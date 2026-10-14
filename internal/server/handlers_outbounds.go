package server

import (
	"net/http"
	"slices"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/axuezi/sing-box-web/internal/dnsconf"
	"github.com/axuezi/sing-box-web/internal/outbounds"
	"github.com/axuezi/sing-box-web/internal/singbox"
	"github.com/axuezi/sing-box-web/internal/store"
)

// changeConfig applies a config change that does not touch users.
func (s *Server) changeConfig(w http.ResponseWriter, r *http.Request, action, target string, build func(cur []byte) ([]byte, error)) {
	s.changeConfigAndUsers(w, r, action, target, func(cur []byte, _ []store.User) ([]byte, []*store.User, error) {
		out, err := build(cur)
		return out, nil, err
	}, nil)
}

func notFound(what string) error {
	return &inputError{status: http.StatusNotFound, code: "not_found", msg: "no such " + what}
}

// referenced refuses to delete something the config still points at.
func referenced(refs []string) error {
	return &inputError{status: http.StatusConflict, code: "referenced",
		msg: "still used by: " + strings.Join(refs, ", "), extra: map[string]any{"references": refs}}
}

// --- outbounds ---

type outboundItem struct {
	Form  outbounds.Form `json:"form"`
	Extra []string       `json:"extra"` // keys kept as they are
	Refs  []string       `json:"refs"`  // where the config uses this outbound
}

type otherItem struct {
	Tag    string   `json:"tag"`
	Type   string   `json:"type"`
	Legacy bool     `json:"legacy,omitempty"` // DNS server in the pre-1.12 format
	Refs   []string `json:"refs"`
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func (s *Server) handleListOutbounds(w http.ResponseWriter, r *http.Request) {
	file, err := singbox.ReadFile(s.opts.SingboxConfig)
	if err != nil {
		writeError(w, http.StatusBadGateway, "config_unreadable", err.Error())
		return
	}
	raw := file.Raw
	items, others := []outboundItem{}, []otherItem{}
	gjson.GetBytes(raw, "outbounds").ForEach(func(_, v gjson.Result) bool {
		tag, typ := v.Get("tag").String(), v.Get("type").String()
		if !slices.Contains(outbounds.Editable, typ) {
			others = append(others, otherItem{Tag: tag, Type: typ, Refs: nonNil(outbounds.References(raw, tag))})
			return true
		}
		f, extra := outbounds.Read(v)
		items = append(items, outboundItem{Form: f, Extra: nonNil(extra), Refs: nonNil(outbounds.References(raw, tag))})
		return true
	})
	gjson.GetBytes(raw, "endpoints").ForEach(func(_, v gjson.Result) bool {
		tag := v.Get("tag").String()
		others = append(others, otherItem{Tag: tag, Type: v.Get("type").String() + " (endpoint)", Refs: nonNil(outbounds.References(raw, tag))})
		return true
	})
	final := gjson.GetBytes(raw, "route.final").String()
	if final == "" {
		final = gjson.GetBytes(raw, "outbounds.0.tag").String()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"outbounds":   items,
		"others":      others,
		"route_final": final,
		"tags":        nonNil(outbounds.Tags(raw)),
		"dns_servers": nonNil(dnsconf.Tags(raw)),
	})
}

// checkOutbound validates what a form points at against the config.
func checkOutbound(cur []byte, f outbounds.Form) error {
	if f.Detour != "" {
		if !slices.Contains(outbounds.Tags(cur), f.Detour) {
			return badInput("detour " + f.Detour + " is not an outbound")
		}
		if outbounds.DetourLoop(cur, f.Tag, f.Detour) {
			return badInput("detour " + f.Detour + " leads back to " + f.Tag)
		}
	}
	if f.Resolver != "" && !slices.Contains(dnsconf.Tags(cur), f.Resolver) {
		return badInput("resolver " + f.Resolver + " is not a DNS server")
	}
	return nil
}

type outboundInput struct {
	Form outbounds.Form `json:"form"`
}

func (s *Server) handleCreateOutbound(w http.ResponseWriter, r *http.Request) {
	var in outboundInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := outbounds.Normalize(&in.Form); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	f := in.Form
	s.changeConfig(w, r, "outbound.create", f.Tag, func(cur []byte) ([]byte, error) {
		if slices.Contains(outbounds.Tags(cur), f.Tag) {
			return nil, &inputError{status: http.StatusConflict, code: "tag_taken", msg: "an outbound with this tag already exists"}
		}
		if err := checkOutbound(cur, f); err != nil {
			return nil, err
		}
		out, err := outbounds.Write(cur, -1, f, gjson.Result{})
		if err != nil {
			return nil, badInput(err.Error())
		}
		return outbounds.SyncStats(out, f.Tag, true)
	})
}

func (s *Server) handleUpdateOutbound(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	var in outboundInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	in.Form.Tag = tag // tags are fixed: routes, detours and stats refer to them
	s.changeConfig(w, r, "outbound.update", tag, func(cur []byte) ([]byte, error) {
		idx, orig := outbounds.Find(cur, tag)
		if idx < 0 {
			return nil, notFound("outbound")
		}
		if in.Form.Type = orig.Get("type").String(); !slices.Contains(outbounds.Editable, in.Form.Type) {
			return nil, badInput(in.Form.Type + " outbounds are not edited here")
		}
		if err := outbounds.Normalize(&in.Form); err != nil {
			return nil, badInput(err.Error())
		}
		if err := checkOutbound(cur, in.Form); err != nil {
			return nil, err
		}
		out, err := outbounds.Write(cur, idx, in.Form, orig)
		if err != nil {
			return nil, badInput(err.Error())
		}
		return out, nil
	})
}

func (s *Server) handleDeleteOutbound(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	s.changeConfig(w, r, "outbound.delete", tag, func(cur []byte) ([]byte, error) {
		idx, orig := outbounds.Find(cur, tag)
		if idx < 0 {
			return nil, notFound("outbound")
		}
		if !slices.Contains(outbounds.Editable, orig.Get("type").String()) {
			return nil, badInput(orig.Get("type").String() + " outbounds are not edited here")
		}
		if refs := outbounds.References(cur, tag); len(refs) > 0 {
			return nil, referenced(refs)
		}
		out, err := outbounds.Remove(cur, idx)
		if err != nil {
			return nil, err
		}
		return outbounds.SyncStats(out, tag, false)
	})
}

// --- DNS ---

type dnsServerItem struct {
	Form  dnsconf.Server `json:"form"`
	Extra []string       `json:"extra"`
	Refs  []string       `json:"refs"`
}

func (s *Server) handleGetDNS(w http.ResponseWriter, r *http.Request) {
	file, err := singbox.ReadFile(s.opts.SingboxConfig)
	if err != nil {
		writeError(w, http.StatusBadGateway, "config_unreadable", err.Error())
		return
	}
	raw := file.Raw
	items, others := []dnsServerItem{}, []otherItem{}
	gjson.GetBytes(raw, "dns.servers").ForEach(func(_, v gjson.Result) bool {
		tag, typ := v.Get("tag").String(), v.Get("type").String()
		refs := nonNil(dnsconf.References(raw, tag))
		if !slices.Contains(dnsconf.Editable, typ) {
			others = append(others, otherItem{Tag: tag, Type: typ, Legacy: typ == "", Refs: refs})
			return true
		}
		f, extra := dnsconf.Read(v)
		items = append(items, dnsServerItem{Form: f, Extra: nonNil(extra), Refs: refs})
		return true
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":  dnsconf.ReadSettings(raw),
		"servers":   items,
		"others":    others,
		"rules":     dnsconf.Rules(raw),
		"outbounds": nonNil(outbounds.Tags(raw)),
	})
}

func (s *Server) handleUpdateDNS(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Settings dnsconf.Settings `json:"settings"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.changeConfig(w, r, "dns.update", "dns", func(cur []byte) ([]byte, error) {
		if err := dnsconf.NormalizeSettings(cur, &in.Settings); err != nil {
			return nil, badInput(err.Error())
		}
		return dnsconf.WriteSettings(cur, in.Settings)
	})
}

// checkDNSServer validates what a server form points at against the config.
func checkDNSServer(cur []byte, f dnsconf.Server) error {
	if f.Detour != "" && !slices.Contains(outbounds.Tags(cur), f.Detour) {
		return badInput("detour " + f.Detour + " is not an outbound")
	}
	if f.Resolver != "" {
		if !slices.Contains(dnsconf.Tags(cur), f.Resolver) {
			return badInput("resolver " + f.Resolver + " is not a DNS server")
		}
		if dnsconf.ResolverLoop(cur, f.Tag, f.Resolver) {
			return badInput("resolver " + f.Resolver + " leads back to " + f.Tag)
		}
	}
	return nil
}

type dnsServerInput struct {
	Form dnsconf.Server `json:"form"`
}

func (s *Server) handleCreateDNSServer(w http.ResponseWriter, r *http.Request) {
	var in dnsServerInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := dnsconf.Normalize(&in.Form); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	f := in.Form
	s.changeConfig(w, r, "dns_server.create", f.Tag, func(cur []byte) ([]byte, error) {
		if slices.Contains(dnsconf.Tags(cur), f.Tag) {
			return nil, &inputError{status: http.StatusConflict, code: "tag_taken", msg: "a DNS server with this tag already exists"}
		}
		if err := checkDNSServer(cur, f); err != nil {
			return nil, err
		}
		out, err := dnsconf.Write(cur, -1, f, gjson.Result{})
		if err != nil {
			return nil, badInput(err.Error())
		}
		return out, nil
	})
}

func (s *Server) handleUpdateDNSServer(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	var in dnsServerInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	in.Form.Tag = tag
	s.changeConfig(w, r, "dns_server.update", tag, func(cur []byte) ([]byte, error) {
		idx, orig := dnsconf.Find(cur, tag)
		if idx < 0 {
			return nil, notFound("DNS server")
		}
		if in.Form.Type = orig.Get("type").String(); !slices.Contains(dnsconf.Editable, in.Form.Type) {
			return nil, badInput("this DNS server type is not edited here")
		}
		if err := dnsconf.Normalize(&in.Form); err != nil {
			return nil, badInput(err.Error())
		}
		if err := checkDNSServer(cur, in.Form); err != nil {
			return nil, err
		}
		out, err := dnsconf.Write(cur, idx, in.Form, orig)
		if err != nil {
			return nil, badInput(err.Error())
		}
		return out, nil
	})
}

func (s *Server) handleDeleteDNSServer(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	s.changeConfig(w, r, "dns_server.delete", tag, func(cur []byte) ([]byte, error) {
		idx, orig := dnsconf.Find(cur, tag)
		if idx < 0 {
			return nil, notFound("DNS server")
		}
		if !slices.Contains(dnsconf.Editable, orig.Get("type").String()) {
			return nil, badInput("this DNS server type is not edited here")
		}
		if refs := dnsconf.References(cur, tag); len(refs) > 0 {
			return nil, referenced(refs)
		}
		return dnsconf.Remove(cur, idx)
	})
}
