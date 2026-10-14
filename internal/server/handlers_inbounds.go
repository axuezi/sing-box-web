package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/axuezi/sing-box-web/internal/apply"
	"github.com/axuezi/sing-box-web/internal/auth"
	"github.com/axuezi/sing-box-web/internal/inbounds"
	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/singbox"
	"github.com/axuezi/sing-box-web/internal/store"
	"github.com/axuezi/sing-box-web/internal/subscription"
)

type inboundItem struct {
	Form   inbounds.Form                `json:"form"`
	Extra  []string                     `json:"extra"` // keys kept as they are
	Users  int                          `json:"users"`
	Export subscription.InboundSettings `json:"export"`
}

type otherInbound struct {
	Tag        string `json:"tag"`
	Type       string `json:"type"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
}

func (s *Server) handleListInbounds(w http.ResponseWriter, r *http.Request) {
	file, err := singbox.ReadFile(s.opts.SingboxConfig)
	if err != nil {
		writeError(w, http.StatusBadGateway, "config_unreadable", err.Error())
		return
	}
	st, err := s.subscriptionSettings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	users, err := s.opts.Store.ListUsers(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	savedFlows, err := protocol.StoredFlows(r.Context(), s.opts.Store)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	flows := protocol.EffectiveFlows(file.Raw, savedFlows)
	items, others := []inboundItem{}, []otherInbound{}
	gjson.GetBytes(file.Raw, "inbounds").ForEach(func(_, v gjson.Result) bool {
		typ := v.Get("type").String()
		if !slices.Contains(inbounds.Editable, typ) {
			others = append(others, otherInbound{Tag: v.Get("tag").String(), Type: typ,
				Listen: v.Get("listen").String(), ListenPort: int(v.Get("listen_port").Int())})
			return true
		}
		f, extra := inbounds.Read(v)
		if f.Type == "vless" {
			f.Flow = flows[f.Tag]
		}
		if extra == nil {
			extra = []string{}
		}
		items = append(items, inboundItem{Form: f, Extra: extra, Users: int(v.Get("users.#").Int()), Export: st.Inbounds[f.Tag]})
		return true
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"inbounds": items, "others": others, "public_host": st.PublicHost, "user_count": len(users),
	})
}

// inputError is a problem with the request found while building the config.
type inputError struct {
	status int
	code   string
	msg    string
	extra  map[string]any
}

func (e *inputError) Error() string { return e.msg }

func badInput(msg string) error {
	return &inputError{status: http.StatusBadRequest, code: "bad_request", msg: msg}
}

// changeConfigAndUsers applies a config change that may also move users between
// inbounds. build gets the current config and users and returns the new
// config and the users to save; after runs once the change is live.
func (s *Server) changeConfigAndUsers(w http.ResponseWriter, r *http.Request, action, target string,
	build func(cur []byte, users []store.User) ([]byte, []*store.User, error), after func(context.Context) error) {
	ctx := context.WithoutCancel(r.Context())
	var upsert []*store.User
	var inputErr error
	res, err := s.opts.Apply.Update(ctx, apply.Change{Actor: s.actor(r), Source: "panel", Summary: action + " " + target},
		func(cur []byte) ([]byte, error) {
			users, err := s.opts.Store.ListUsers(ctx)
			if err != nil {
				return nil, err
			}
			out, up, err := build(cur, users)
			if err != nil {
				inputErr = err
				return nil, err
			}
			upsert = up
			return protocol.RenderManaged(ctx, s.opts.Store, out, flatten(mergeUsers(users, upsert, nil)))
		},
		func(ctx context.Context) error {
			if err := s.opts.Store.SaveUsers(ctx, upsert, nil); err != nil {
				return err
			}
			if after != nil {
				return after(ctx)
			}
			return nil
		})
	if inputErr != nil {
		var ie *inputError
		if errors.As(inputErr, &ie) {
			body := map[string]any{"code": ie.code, "error": ie.msg}
			for k, v := range ie.extra {
				body[k] = v
			}
			writeJSON(w, ie.status, body)
		} else {
			writeError(w, http.StatusBadRequest, "bad_request", inputErr.Error())
		}
		return
	}
	if !s.applyFailed(w, r, err, res, action, target) {
		s.audit(ctx, s.actor(r), s.clientIP(r), action, target, res.Status)
		writeJSON(w, http.StatusOK, map[string]any{"result": res})
	}
}

// checkPort refuses a port another inbound or another program on the host
// already uses: sing-box would fail to bind it and exit on reload.
func (s *Server) checkPort(cur []byte, f inbounds.Form) error {
	if other := inbounds.Conflict(cur, f); other != "" {
		return &inputError{status: http.StatusConflict, code: "port_taken", msg: "port already used by inbound " + other,
			extra: map[string]any{"inbound": other}}
	}
	if socks, ok := s.opts.Sockets(); ok {
		if who := inbounds.PortInUse(cur, f, socks); who != "" {
			return &inputError{status: http.StatusConflict, code: "port_in_use",
				msg: "port is already used by another program on this host (" + who + ")", extra: map[string]any{"socket": who}}
		}
	}
	return nil
}

// saveExport stores how an inbound is exported to subscriptions; nil
// removes its overrides.
func (s *Server) saveExport(ctx context.Context, tag string, e *subscription.InboundSettings) error {
	st, err := s.subscriptionSettings(ctx)
	if err != nil {
		return err
	}
	if st.Inbounds == nil {
		st.Inbounds = map[string]subscription.InboundSettings{}
	}
	if e == nil {
		delete(st.Inbounds, tag)
	} else {
		st.Inbounds[tag] = *e
	}
	b, _ := json.Marshal(st)
	return s.opts.Store.SetSetting(ctx, subscriptionSettingsKey, string(b))
}

type inboundInput struct {
	Form        inbounds.Form                 `json:"form"`
	AssignUsers bool                          `json:"assign_users"` // create: enable for every user
	Export      *subscription.InboundSettings `json:"export"`
}

func (in *inboundInput) normalize() error {
	if err := inbounds.Normalize(&in.Form); err != nil {
		return err
	}
	if e := in.Export; e != nil {
		e.Host, e.Name = strings.TrimSpace(e.Host), strings.TrimSpace(e.Name)
		if e.Port < 0 || e.Port > 65535 {
			return errors.New("client port must be 0-65535")
		}
	}
	return nil
}

func (s *Server) handleCreateInbound(w http.ResponseWriter, r *http.Request) {
	var in inboundInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := in.normalize(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	f := in.Form
	ctx := context.WithoutCancel(r.Context())
	s.changeConfigAndUsers(w, r, "inbound.create", f.Tag, func(cur []byte, users []store.User) ([]byte, []*store.User, error) {
		if idx, _ := inbounds.Find(cur, f.Tag); idx >= 0 {
			return nil, nil, &inputError{status: http.StatusConflict, code: "tag_taken", msg: "an inbound with this tag already exists"}
		}
		if err := s.checkPort(cur, f); err != nil {
			return nil, nil, err
		}
		out, err := inbounds.Write(cur, -1, f, gjson.Result{})
		if err != nil {
			return nil, nil, badInput(err.Error())
		}
		if out, err = inbounds.SyncStatsInbounds(out, f.Tag, true); err != nil {
			return nil, nil, err
		}
		var upsert []*store.User
		if in.AssignUsers {
			for _, u := range users {
				u.Inbounds = append(u.Inbounds, f.Tag)
				upsert = append(upsert, &u)
			}
		}
		// Render now so the new entries carry the chosen flow rather than
		// the default; the final render copies it from the first entry.
		if out, err = protocol.RenderManaged(ctx, s.opts.Store, out, flatten(mergeUsers(users, upsert, nil))); err != nil {
			return nil, nil, err
		}
		if out, err = inbounds.SetFlow(out, f.Tag, f.Flow); err != nil {
			return nil, nil, err
		}
		return out, upsert, nil
	}, func(ctx context.Context) error {
		if in.Form.Type == "vless" {
			if err := protocol.SetStoredFlow(ctx, s.opts.Store, in.Form.Tag, in.Form.Flow); err != nil {
				return err
			}
		}
		if in.Export == nil {
			return nil
		}
		return s.saveExport(ctx, f.Tag, in.Export)
	})
}

func (s *Server) handleUpdateInbound(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	var in inboundInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	in.Form.Tag = tag // tags are fixed: users, rules and subscriptions refer to them
	s.changeConfigAndUsers(w, r, "inbound.update", tag, func(cur []byte, _ []store.User) ([]byte, []*store.User, error) {
		idx, orig := inbounds.Find(cur, tag)
		if idx < 0 {
			return nil, nil, &inputError{status: http.StatusNotFound, code: "not_found", msg: "no such inbound"}
		}
		in.Form.Type = orig.Get("type").String()
		if err := in.normalize(); err != nil {
			return nil, nil, badInput(err.Error())
		}
		if err := s.checkPort(cur, in.Form); err != nil {
			return nil, nil, err
		}
		out, err := inbounds.Write(cur, idx, in.Form, orig)
		if err != nil {
			return nil, nil, badInput(err.Error())
		}
		return out, nil, nil
	}, func(ctx context.Context) error {
		if in.Form.Type == "vless" {
			if err := protocol.SetStoredFlow(ctx, s.opts.Store, in.Form.Tag, in.Form.Flow); err != nil {
				return err
			}
		}
		if in.Export == nil {
			return nil
		}
		return s.saveExport(ctx, tag, in.Export)
	})
}

func (s *Server) handleDeleteInbound(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	s.changeConfigAndUsers(w, r, "inbound.delete", tag, func(cur []byte, users []store.User) ([]byte, []*store.User, error) {
		idx, orig := inbounds.Find(cur, tag)
		if idx < 0 {
			return nil, nil, &inputError{status: http.StatusNotFound, code: "not_found", msg: "no such inbound"}
		}
		if !slices.Contains(inbounds.Editable, orig.Get("type").String()) {
			return nil, nil, badInput("only hysteria2 and vless inbounds are managed here; edit others in the config editor")
		}
		if refs := inbounds.References(cur, tag); len(refs) > 0 {
			return nil, nil, &inputError{status: http.StatusConflict, code: "referenced",
				msg: "the inbound is used by rules: " + strings.Join(refs, ", "), extra: map[string]any{"references": refs}}
		}
		out, err := inbounds.Remove(cur, idx)
		if err != nil {
			return nil, nil, err
		}
		if out, err = inbounds.SyncStatsInbounds(out, tag, false); err != nil {
			return nil, nil, err
		}
		var upsert []*store.User
		for _, u := range users {
			if i := slices.Index(u.Inbounds, tag); i >= 0 {
				u.Inbounds = slices.Delete(slices.Clone(u.Inbounds), i, i+1)
				upsert = append(upsert, &u)
			}
		}
		return out, upsert, nil
	}, func(ctx context.Context) error { return s.saveExport(ctx, tag, nil) })
}

func (s *Server) handleGenerateInbound(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind       string `json:"kind"`
		ServerName string `json:"server_name"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	switch in.Kind {
	case "reality_keypair":
		priv, pub, err := inbounds.RealityKeyPair()
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"private_key": priv, "public_key": pub})
	case "short_id":
		id, err := inbounds.ShortID()
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"short_id": id})
	case "self_signed":
		name := strings.TrimSpace(in.ServerName)
		if name == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "server_name is required")
			return
		}
		cert, key, err := inbounds.SelfSigned(name)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"certificate": cert, "key": key})
	case "password":
		writeJSON(w, http.StatusOK, map[string]string{"password": auth.RandomToken(18)})
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "kind must be reality_keypair, short_id, self_signed or password")
	}
}
