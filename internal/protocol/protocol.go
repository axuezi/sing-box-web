// Package protocol knows, per sing-box inbound type, how users are written
// into config.json and how a client connects to that inbound.
package protocol

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/tidwall/gjson"

	"github.com/axuezi/sing-box-web/internal/store"
)

// ErrUnsupported is returned for inbound settings a protocol cannot export
// to clients yet (e.g. a transport it does not know).
var ErrUnsupported = errors.New("unsupported inbound settings")

// Endpoint is how clients reach an inbound from the outside. It may differ
// from the listen address, e.g. behind an SNI router.
type Endpoint struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Insecure bool   `json:"insecure"` // skip TLS verification (self-signed certs)
}

// Inbound is one inbound from config.json.
type Inbound struct {
	Index int
	Tag   string
	Type  string
	Raw   gjson.Result
}

// Client is everything needed to describe one user's connection to one
// inbound in a client config.
type Client struct {
	Flow     *string // explicit persisted VLESS flow, including empty
	Name     string  // proxy name shown in the client
	Inbound  Inbound
	Endpoint Endpoint
	User     store.User
}

// Protocol handles one inbound type.
type Protocol interface {
	Type() string
	// CredentialKeys are the user-object keys holding credentials.
	CredentialKeys() []string
	// UserFields returns the credential fields of a config user entry.
	UserFields(u store.User) []KV
	// DefaultUserFields are non-credential fields new users get when the
	// inbound has no existing user to copy them from (e.g. VLESS flow).
	DefaultUserFields(in Inbound) []KV
	// Import reads credentials from an existing config user entry.
	Import(entry gjson.Result) (password, uuid string)
	// Mihomo returns a Clash.Meta / mihomo proxy object.
	Mihomo(c Client) (map[string]any, error)
	// SingBox returns a sing-box outbound object.
	SingBox(c Client) (map[string]any, error)
	// Link returns a share link (hysteria2://, vless://, ...).
	Link(c Client) (string, error)
}

// KV is an ordered JSON object member.
type KV struct {
	Key   string
	Value any
}

var registry = map[string]Protocol{}

func register(p Protocol) { registry[p.Type()] = p }

// Get returns the protocol for an inbound type.
func Get(typ string) (Protocol, bool) {
	p, ok := registry[typ]
	return p, ok
}

// Supported lists the inbound types with user management.
func Supported() []string {
	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Inbounds returns the managed (supported-type) inbounds in a config.
func Inbounds(raw []byte) []Inbound {
	var out []Inbound
	gjson.GetBytes(raw, "inbounds").ForEach(func(k, v gjson.Result) bool {
		typ := v.Get("type").String()
		if _, ok := registry[typ]; ok {
			out = append(out, Inbound{Index: int(k.Int()), Tag: v.Get("tag").String(), Type: typ, Raw: v})
		}
		return true
	})
	return out
}

// NewUUID returns a random RFC 4122 version 4 UUID.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// marshalOrdered encodes members as a JSON object in the given order.
func marshalOrdered(kvs []KV) ([]byte, error) {
	buf := []byte{'{'}
	for i, kv := range kvs {
		if i > 0 {
			buf = append(buf, ',')
		}
		k, _ := json.Marshal(kv.Key)
		v, err := json.Marshal(kv.Value)
		if err != nil {
			return nil, err
		}
		buf = append(buf, k...)
		buf = append(buf, ':')
		buf = append(buf, v...)
	}
	return append(buf, '}'), nil
}
