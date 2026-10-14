package protocol

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/pretty"
	"github.com/tidwall/sjson"

	"github.com/axuezi/sing-box-web/internal/auth"
	"github.com/axuezi/sing-box-web/internal/store"
)

// Render writes users into every managed inbound's "users" list and keeps
// experimental.v2ray_api.stats.users in step. Everything else in raw is
// preserved, and the result is formatted with raw's own indentation.
//
// Non-credential fields of an inbound's first existing user (e.g. VLESS
// "flow") are copied onto every rendered user, so per-inbound user settings
// made by hand survive.
func Render(raw []byte, users []store.User, flows ...map[string]string) ([]byte, error) {
	out := raw
	for _, in := range Inbounds(raw) {
		p, _ := Get(in.Type)
		extra := templateFields(in, p)
		if in.Type == "vless" && len(flows) > 0 {
			if flow, ok := flows[0][in.Tag]; ok {
				kept := make([]KV, 0, len(extra)+1)
				for _, kv := range extra {
					if kv.Key != "flow" {
						kept = append(kept, kv)
					}
				}
				if flow != "" {
					kept = append(kept, KV{"flow", flow})
				}
				extra = kept
			}
		}
		var entries [][]byte
		for _, u := range users {
			if !u.Active() || !u.HasInbound(in.Tag) {
				continue
			}
			kvs := append([]KV{{"name", u.Name}}, p.UserFields(u)...)
			kvs = append(kvs, extra...)
			b, err := marshalOrdered(kvs)
			if err != nil {
				return nil, err
			}
			entries = append(entries, b)
		}
		arr := "[" + string(bytes.Join(entries, []byte(","))) + "]"
		var err error
		out, err = sjson.SetRawBytes(out, "inbounds."+strconv.Itoa(in.Index)+".users", []byte(arr))
		if err != nil {
			return nil, fmt.Errorf("inbound %s: %w", in.Tag, err)
		}
	}

	if gjson.GetBytes(out, "experimental.v2ray_api.stats").Exists() {
		names := []string{}
		for _, u := range users {
			if u.Active() {
				names = append(names, u.Name)
			}
		}
		var err error
		if out, err = sjson.SetBytes(out, "experimental.v2ray_api.stats.users", names); err != nil {
			return nil, err
		}
	}
	return FormatLike(raw, out), nil
}

// templateFields returns the non-name, non-credential fields new user
// entries of an inbound should carry.
func templateFields(in Inbound, p Protocol) []KV {
	first := in.Raw.Get("users.0")
	if !first.Exists() {
		return p.DefaultUserFields(in)
	}
	skip := map[string]bool{"name": true}
	for _, k := range p.CredentialKeys() {
		skip[k] = true
	}
	var kvs []KV
	first.ForEach(func(k, v gjson.Result) bool {
		if !skip[k.String()] {
			kvs = append(kvs, KV{k.String(), v.Value()})
		}
		return true
	})
	return kvs
}

// FormatLike pretty-prints doc using the indentation found in like
// (two spaces, four spaces or a tab), one value per line, keys unsorted.
func FormatLike(like, doc []byte) []byte {
	indent := "  "
	for _, line := range strings.Split(string(like), "\n")[1:] {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && len(trimmed) < len(line) {
			indent = line[:len(line)-len(trimmed)]
			break
		}
	}
	return pretty.PrettyOptions(pretty.Ugly(doc), &pretty.Options{Indent: indent, Width: 0})
}

// Import builds panel users from the users already present in a config.
// Entries with the same "name" across inbounds become one user; unnamed
// entries are matched by position (the first unnamed entry of each inbound
// becomes "user-1", and so on). Credentials missing for some protocol are
// generated.
func Import(raw []byte) []store.User {
	byName := map[string]*store.User{}
	var order []string
	for _, in := range Inbounds(raw) {
		p, _ := Get(in.Type)
		unnamed := 0
		in.Raw.Get("users").ForEach(func(_, e gjson.Result) bool {
			name := e.Get("name").String()
			if name == "" {
				unnamed++
				name = "user-" + strconv.Itoa(unnamed)
			}
			u, ok := byName[name]
			if !ok {
				u = &store.User{Name: name, Enabled: true, Inbounds: []string{}}
				byName[name] = u
				order = append(order, name)
			}
			pw, id := p.Import(e)
			if pw != "" && u.Password == "" {
				u.Password = pw
			}
			if id != "" && u.UUID == "" {
				u.UUID = id
			}
			if !u.HasInbound(in.Tag) {
				u.Inbounds = append(u.Inbounds, in.Tag)
			}
			return true
		})
	}
	out := make([]store.User, 0, len(order))
	for _, name := range order {
		u := byName[name]
		FillCredentials(u)
		out = append(out, *u)
	}
	return out
}

// FillCredentials generates any missing password, UUID or subscription token.
func FillCredentials(u *store.User) {
	if u.Password == "" {
		u.Password = auth.RandomToken(18)
	}
	if u.UUID == "" {
		u.UUID = NewUUID()
	}
	if u.SubToken == "" {
		u.SubToken = auth.RandomToken(24)
	}
}
