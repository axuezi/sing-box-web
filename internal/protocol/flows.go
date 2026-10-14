package protocol

import (
	"context"
	"encoding/json"
	"github.com/axuezi/sing-box-web/internal/store"
)

const flowsKey = "inbound_vless_flows"

// StoredFlows distinguishes an explicit empty flow from an unset preference.
func StoredFlows(ctx context.Context, st *store.Store) (map[string]string, error) {
	raw, err := st.GetSetting(ctx, flowsKey)
	if err != nil {
		return nil, err
	}
	flows := map[string]string{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &flows); err != nil {
			return nil, err
		}
	}
	if flows == nil {
		flows = map[string]string{}
	}
	return flows, nil
}
func writeFlows(ctx context.Context, st *store.Store, flows map[string]string) error {
	b, err := json.Marshal(flows)
	if err != nil {
		return err
	}
	return st.SetSetting(ctx, flowsKey, string(b))
}
func SetStoredFlow(ctx context.Context, st *store.Store, tag, flow string) error {
	flows, err := StoredFlows(ctx, st)
	if err != nil {
		return err
	}
	flows[tag] = flow
	return writeFlows(ctx, st, flows)
}

// EffectiveFlows adopts explicit user fields from the config, retaining saved
// preferences when all users have been removed. No private metadata goes into sing-box JSON.
func EffectiveFlows(raw []byte, saved map[string]string) map[string]string {
	out := map[string]string{}
	for _, in := range Inbounds(raw) {
		if in.Type != "vless" {
			continue
		}
		if first := in.Raw.Get("users.0"); first.Exists() {
			out[in.Tag] = first.Get("flow").String()
		} else if flow, ok := saved[in.Tag]; ok {
			out[in.Tag] = flow
		} else {
			out[in.Tag] = ""
			p, _ := Get(in.Type)
			for _, kv := range p.DefaultUserFields(in) {
				if kv.Key == "flow" {
					out[in.Tag], _ = kv.Value.(string)
				}
			}
		}
	}
	return out
}

// SaveInboundFlows is called in the business transaction, before explicit form overrides.
func SaveInboundFlows(ctx context.Context, st *store.Store, previous, candidate []byte) error {
	saved, err := StoredFlows(ctx, st)
	if err != nil {
		return err
	}
	return writeFlows(ctx, st, EffectiveFlows(candidate, EffectiveFlows(previous, saved)))
}
func RenderManaged(ctx context.Context, st *store.Store, raw []byte, users []store.User) ([]byte, error) {
	saved, err := StoredFlows(ctx, st)
	if err != nil {
		return nil, err
	}
	return Render(raw, users, EffectiveFlows(raw, saved))
}
