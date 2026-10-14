package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/axuezi/sing-box-web/internal/apply"
	"github.com/axuezi/sing-box-web/internal/auth"
	"github.com/axuezi/sing-box-web/internal/protocol"
	"github.com/axuezi/sing-box-web/internal/singbox"
	"github.com/axuezi/sing-box-web/internal/store"
)

// userView is a user without credentials, for lists.
type userView struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Note      string    `json:"note"`
	Enabled   bool      `json:"enabled"`
	Inbounds  []string  `json:"inbounds"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	LastSubAt time.Time `json:"last_sub_at,omitzero"`
	// MonthUp/MonthDown are this month's bytes, when stats are available.
	MonthUp   int64 `json:"month_up"`
	MonthDown int64 `json:"month_down"`

	ExpiresAt  time.Time `json:"expires_at,omitzero"`
	QuotaBytes int64     `json:"quota_bytes"`
	ResetDay   int       `json:"reset_day"`
	CycleStart time.Time `json:"cycle_start,omitzero"`
	NextReset  time.Time `json:"next_reset"`
	CycleUp    int64     `json:"cycle_up"`
	CycleDown  int64     `json:"cycle_down"`
	Limited    string    `json:"limited"`

	// Online is "online", "offline" or "unknown" (connections exist that
	// could be this user's but cannot be matched). Conns counts the live
	// connections matched to the user.
	Online       string    `json:"online"`
	Conns        int       `json:"conns"`
	LastActiveAt time.Time `json:"last_active_at,omitzero"`
}

func viewOf(u store.User) userView {
	return userView{ID: u.ID, Name: u.Name, Note: u.Note, Enabled: u.Enabled, Inbounds: u.Inbounds,
		CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, LastSubAt: u.LastSubAt,
		ExpiresAt: u.ExpiresAt, QuotaBytes: u.QuotaBytes, ResetDay: u.ResetDay, CycleStart: u.CycleStart,
		NextReset: store.NextCycle(time.Now(), u.ResetDay, time.Local).UTC(),
		CycleUp:   u.CycleUp, CycleDown: u.CycleDown, Limited: u.Limited, LastActiveAt: u.LastActiveAt}
}

type inboundOption struct {
	Tag  string `json:"tag"`
	Type string `json:"type"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.opts.Store.ListUsers(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	month := map[string]dayTotal{}
	if _, series, err := s.trafficSeries(r.Context(), 1); err == nil {
		for _, sv := range series {
			if sv.Kind == "user" {
				month[sv.Key] = sv.Month
			}
		}
	}
	live := s.livePresence(r.Context(), users)
	var seen map[string]time.Time
	if s.opts.Presence != nil {
		seen = s.opts.Presence.LastSeen()
	}
	views := make([]userView, 0, len(users))
	for _, u := range users {
		v := viewOf(u)
		m := month[strconv.FormatInt(u.ID, 10)]
		v.MonthUp, v.MonthDown = m.Up, m.Down
		if t := seen[u.Name]; t.After(v.LastActiveAt) {
			v.LastActiveAt = t.UTC().Truncate(time.Second)
		}
		v.Online, v.Conns = live.state(u)
		views = append(views, v)
	}
	inbounds := []inboundOption{}
	if f, err := singbox.ReadFile(s.opts.SingboxConfig); err == nil {
		for _, in := range protocol.Inbounds(f.Raw) {
			inbounds = append(inbounds, inboundOption{Tag: in.Tag, Type: in.Type})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": views, "inbounds": inbounds, "supported_types": protocol.Supported(),
		"presence": live.summary(s.opts.Presence)})
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`)

type userInput struct {
	Name     *string   `json:"name"`
	Note     *string   `json:"note"`
	Enabled  *bool     `json:"enabled"`
	Inbounds *[]string `json:"inbounds"`
	// ExpiresAt is unix seconds; 0 clears it. QuotaBytes 0 = unlimited.
	ExpiresAt  *int64 `json:"expires_at"`
	QuotaBytes *int64 `json:"quota_bytes"`
	ResetDay   *int   `json:"reset_day"`
}

// applyInput validates and copies set fields of in onto u.
func applyInput(u *store.User, in userInput, validTags []string) error {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if !validName.MatchString(n) {
			return errors.New("name must be 1-64 characters: letters, digits, . _ @ -, starting with a letter or digit")
		}
		u.Name = n
	}
	if in.Note != nil {
		u.Note = truncate(strings.TrimSpace(*in.Note), 500)
	}
	if in.Enabled != nil {
		u.Enabled = *in.Enabled
	}
	if in.Inbounds != nil {
		tags := []string{}
		for _, t := range *in.Inbounds {
			if !slices.Contains(validTags, t) {
				return fmt.Errorf("unknown inbound %q", t)
			}
			if !slices.Contains(tags, t) {
				tags = append(tags, t)
			}
		}
		u.Inbounds = tags
	}
	if in.ExpiresAt != nil {
		switch e := *in.ExpiresAt; {
		case e < 0:
			return errors.New("expires_at must be unix seconds, or 0 for never")
		case e == 0:
			u.ExpiresAt = time.Time{}
		default:
			u.ExpiresAt = time.Unix(e, 0).UTC()
		}
	}
	if in.QuotaBytes != nil {
		if *in.QuotaBytes < 0 {
			return errors.New("quota_bytes must be 0 (unlimited) or more")
		}
		u.QuotaBytes = *in.QuotaBytes
	}
	if in.ResetDay != nil {
		if *in.ResetDay < 1 || *in.ResetDay > 28 {
			return errors.New("reset_day must be between 1 and 28")
		}
		u.ResetDay = *in.ResetDay
	}
	return nil
}

// mutateUsers applies a change to the user list: it renders the new user
// set into the config and applies it; the database is updated only once
// the config is live. mutate receives a copy of all users and returns the
// users to save and the ids to delete.
//
// Limits are re-evaluated for the users being saved, so a renewal or a
// usage reset takes effect at once. afterCommit, if set, runs once the users
// are saved.
func (s *Server) mutateUsers(w http.ResponseWriter, r *http.Request, action, target string,
	mutate func(users []store.User, tags []string) (upsert []*store.User, deleted []int64, err error),
	afterCommit ...func(context.Context) error) {
	ctx := context.WithoutCancel(r.Context())
	var upsert []*store.User
	var deleted []int64
	var inputErr error
	// Routes address a user by ID; the audit log names them.
	label := target

	build := func(cur []byte) ([]byte, error) {
		users, err := s.opts.Store.ListUsers(ctx)
		if err != nil {
			return nil, err
		}
		if id, err := strconv.ParseInt(target, 10, 64); err == nil {
			for _, u := range users {
				if u.ID == id {
					label = u.Name
				}
			}
		}
		var tags []string
		for _, in := range protocol.Inbounds(cur) {
			tags = append(tags, in.Tag)
		}
		upsert, deleted, inputErr = mutate(users, tags)
		if inputErr != nil {
			return nil, inputErr
		}
		now := time.Now()
		for _, u := range upsert {
			u.Limited = u.LimitReason(now)
		}
		next := mergeUsers(users, upsert, deleted)
		for _, a := range next {
			for _, b := range next {
				if a.Name == b.Name && a != b {
					inputErr = store.ErrConflict
					return nil, inputErr
				}
			}
		}
		return protocol.RenderManaged(ctx, s.opts.Store, cur, flatten(next))
	}
	commit := func(ctx context.Context) error {
		if err := s.opts.Store.SaveUsers(ctx, upsert, deleted); err != nil {
			return err
		}
		for _, f := range afterCommit {
			if err := f(ctx); err != nil {
				return err
			}
		}
		return nil
	}

	res, err := s.opts.Apply.Update(ctx, apply.Change{Actor: s.actor(r), Source: "panel", Summary: action + " " + target, RequireUsage: len(afterCommit) > 0}, build, commit)
	if inputErr != nil {
		switch {
		case errors.Is(inputErr, store.ErrConflict):
			writeError(w, http.StatusConflict, "name_taken", "a user with this name already exists")
		case errors.Is(inputErr, errNoUser):
			writeError(w, http.StatusNotFound, "not_found", "no such user")
		default:
			writeError(w, http.StatusBadRequest, "bad_request", inputErr.Error())
		}
		return
	}
	if !s.applyFailed(w, r, err, res, action, label) {
		s.audit(ctx, s.actor(r), s.clientIP(r), action, label, res.Status)
		var saved []userView
		for _, u := range upsert {
			saved = append(saved, viewOf(*u))
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": res, "users": saved})
	}
}

func mergeUsers(users []store.User, upsert []*store.User, deleted []int64) []*store.User {
	byID := map[int64]*store.User{}
	var out []*store.User
	for i := range users {
		u := users[i]
		if slices.Contains(deleted, u.ID) {
			continue
		}
		p := &u
		byID[u.ID] = p
		out = append(out, p)
	}
	for _, u := range upsert {
		if u.ID != 0 {
			if p, ok := byID[u.ID]; ok {
				*p = *u
				continue
			}
		}
		out = append(out, u)
	}
	return out
}

func flatten(ps []*store.User) []store.User {
	out := make([]store.User, len(ps))
	for i, p := range ps {
		out[i] = *p
	}
	return out
}

// applyFailed writes the error response for a failed apply and reports
// whether it did.
func (s *Server) applyFailed(w http.ResponseWriter, r *http.Request, err error, res apply.Result, action, target string) bool {
	if err == nil {
		return false
	}
	var ce *apply.CheckError
	switch {
	case errors.As(err, &ce):
		s.audit(r.Context(), s.actor(r), s.clientIP(r), "config.rejected", target, truncate(ce.Output, 500))
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"code": "check_failed", "error": "sing-box rejected the config", "output": ce.Output})
	case errors.Is(err, apply.ErrStale):
		writeError(w, http.StatusConflict, "stale", err.Error())
	case errors.Is(err, apply.ErrUsageUnavailable):
		writeError(w, http.StatusServiceUnavailable, "usage_unavailable", err.Error())
	case errors.Is(err, apply.ErrRecoveryFailed):
		s.audit(r.Context(), s.actor(r), s.clientIP(r), "config.recovery_failed", target, action)
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "recovery_failed", "error": err.Error(), "result": res})
	case errors.Is(err, apply.ErrRolledBack):
		s.audit(r.Context(), s.actor(r), s.clientIP(r), "config.auto_rollback", target, action)
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "rolled_back", "error": err.Error(), "result": res})
	default:
		s.internalError(w, r, err)
	}
	return true
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var in userInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if in.Name == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	s.mutateUsers(w, r, "user.create", *in.Name, func(_ []store.User, tags []string) ([]*store.User, []int64, error) {
		u := &store.User{Enabled: true, Inbounds: tags}
		if err := applyInput(u, in, tags); err != nil {
			return nil, nil, err
		}
		protocol.FillCredentials(u)
		return []*store.User{u}, nil, nil
	})
}

func (s *Server) userFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid user id")
		return 0, false
	}
	return id, true
}

func findUser(users []store.User, id int64) (*store.User, error) {
	for i := range users {
		if users[i].ID == id {
			u := users[i]
			return &u, nil
		}
	}
	return nil, errNoUser
}

var errNoUser = errors.New("no such user")

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := s.userFromPath(w, r)
	if !ok {
		return
	}
	var in userInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.mutateUsers(w, r, "user.update", r.PathValue("id"), func(users []store.User, tags []string) ([]*store.User, []int64, error) {
		u, err := findUser(users, id)
		if err != nil {
			return nil, nil, err
		}
		if err := applyInput(u, in, tags); err != nil {
			return nil, nil, err
		}
		return []*store.User{u}, nil, nil
	})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := s.userFromPath(w, r)
	if !ok {
		return
	}
	s.mutateUsers(w, r, "user.delete", r.PathValue("id"), func(users []store.User, _ []string) ([]*store.User, []int64, error) {
		if _, err := findUser(users, id); err != nil {
			return nil, nil, err
		}
		return nil, []int64{id}, nil
	})
}

type resetInput struct {
	Password bool `json:"password"`
	UUID     bool `json:"uuid"`
	Token    bool `json:"token"`
}

func (s *Server) handleResetUser(w http.ResponseWriter, r *http.Request) {
	id, ok := s.userFromPath(w, r)
	if !ok {
		return
	}
	var in resetInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	parts := []string{}
	s.mutateUsers(w, r, "user.reset", r.PathValue("id"), func(users []store.User, _ []string) ([]*store.User, []int64, error) {
		u, err := findUser(users, id)
		if err != nil {
			return nil, nil, err
		}
		if in.Password {
			u.Password = auth.RandomToken(18)
			parts = append(parts, "password")
		}
		if in.UUID {
			u.UUID = protocol.NewUUID()
			parts = append(parts, "uuid")
		}
		if in.Token {
			u.SubToken = auth.RandomToken(24)
			parts = append(parts, "token")
		}
		if len(parts) == 0 {
			return nil, nil, errors.New("nothing to reset")
		}
		return []*store.User{u}, nil, nil
	})
}

type renewInput struct {
	Months     int  `json:"months"`
	Days       int  `json:"days"`
	ResetUsage bool `json:"reset_usage"`
}

// handleRenewUser extends a user's expiry by months and days, counted from
// the current expiry or, if that has passed or is unset, from now.
func (s *Server) handleRenewUser(w http.ResponseWriter, r *http.Request) {
	id, ok := s.userFromPath(w, r)
	if !ok {
		return
	}
	var in renewInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if in.Months < 0 || in.Days < 0 || in.Months > 120 || in.Days > 3660 || in.Months+in.Days == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "renew by 0-120 months and/or 0-3660 days")
		return
	}
	now := time.Now()
	var after []func(context.Context) error
	if in.ResetUsage {
		after = append(after, func(ctx context.Context) error { return s.opts.Store.ResetCycle(ctx, id, now) })
	}
	s.mutateUsers(w, r, "user.renew", r.PathValue("id"), func(users []store.User, _ []string) ([]*store.User, []int64, error) {
		now = time.Now()
		u, err := findUser(users, id)
		if err != nil {
			return nil, nil, err
		}
		from := u.ExpiresAt
		if from.Before(now) {
			from = now
		}
		u.ExpiresAt = from.In(time.Local).AddDate(0, in.Months, in.Days).UTC().Truncate(time.Second)
		if in.ResetUsage {
			zeroCycle(u, now)
		}
		return []*store.User{u}, nil, nil
	}, after...)
}

// handleResetUsage starts a new quota cycle for a user now.
func (s *Server) handleResetUsage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.userFromPath(w, r)
	if !ok {
		return
	}
	now := time.Now()
	s.mutateUsers(w, r, "user.reset_usage", r.PathValue("id"), func(users []store.User, _ []string) ([]*store.User, []int64, error) {
		now = time.Now()
		u, err := findUser(users, id)
		if err != nil {
			return nil, nil, err
		}
		zeroCycle(u, now)
		return []*store.User{u}, nil, nil
	}, func(ctx context.Context) error { return s.opts.Store.ResetCycle(ctx, id, now) })
}

// zeroCycle mirrors store.ResetCycle on the in-memory copy so the limit is
// re-evaluated against the fresh cycle.
func zeroCycle(u *store.User, now time.Time) {
	u.CycleStart, u.CycleUp, u.CycleDown = now.UTC().Truncate(time.Second), 0, 0
}

// handleUserClient returns everything needed to hand a user their
// connection: subscription URLs and share links. These contain credentials.
func (s *Server) handleUserClient(w http.ResponseWriter, r *http.Request) {
	id, ok := s.userFromPath(w, r)
	if !ok {
		return
	}
	u, err := s.opts.Store.GetUser(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no such user")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	file, err := singbox.ReadFile(s.opts.SingboxConfig)
	if err != nil {
		writeError(w, http.StatusBadGateway, "config_unreadable", err.Error())
		return
	}
	settings, err := s.subscriptionSettings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	clients, skipped, err := s.subscriptionClients(r.Context(), file.Raw, u, settings)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	type link struct {
		Name    string `json:"name"`
		Inbound string `json:"inbound"`
		URL     string `json:"url,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	links := []link{}
	for _, c := range clients {
		p, _ := protocol.Get(c.Inbound.Type)
		l, err := p.Link(c)
		item := link{Name: c.Name, Inbound: c.Inbound.Tag, URL: l}
		if err != nil {
			item.Error = err.Error()
		}
		links = append(links, item)
	}
	base := s.publicBaseURL(r) + "/sub/" + u.SubToken
	writeJSON(w, http.StatusOK, map[string]any{
		"subscription": map[string]string{
			"auto":    base,
			"mihomo":  base + "?format=mihomo",
			"singbox": base + "?format=singbox",
			"base64":  base + "?format=base64",
		},
		"links":   links,
		"skipped": skipped,
		"enabled": u.Enabled,
	})
}

func (s *Server) publicBaseURL(r *http.Request) string {
	scheme := "http"
	if s.isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + s.requestHost(r)
}
