package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"seed/kernel/permissions"
	"seed/kernel/tools"
)

type outboundRequest struct {
	Hosts   []string `json:"hosts"`
	Secrets []string `json:"secrets"`
	Reason  string   `json:"reason"`
}

// parse validates a request. It keeps everything requested, granted or not:
// the owner approves exactly this list, and exactly this list is granted (no
// re-check later that could grant what the owner didn't see).
func (e *Egress) parse(input json.RawMessage) (hosts, secrets []string, reason string, err error) {
	var req outboundRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, nil, "", err
	}
	reason = strings.TrimSpace(req.Reason)
	if reason == "" {
		return nil, nil, "", fmt.Errorf("say why the organism needs this (reason)")
	}
	if len(reason) > 500 {
		return nil, nil, "", fmt.Errorf("keep the reason under 500 characters")
	}
	if len(req.Hosts)+len(req.Secrets) == 0 || len(req.Hosts)+len(req.Secrets) > 10 {
		return nil, nil, "", fmt.Errorf("ask for 1 to 10 hosts and secrets at a time")
	}
	seen := map[string]bool{}
	for _, h := range req.Hosts {
		v, err := ValidateHostPattern(h)
		if err != nil {
			return nil, nil, "", err
		}
		if !seen["h"+v] {
			seen["h"+v] = true
			hosts = append(hosts, v)
		}
	}
	for _, s := range req.Secrets {
		v, err := ValidateSecretName(s)
		if err != nil {
			return nil, nil, "", err
		}
		if !seen["s"+v] {
			seen["s"+v] = true
			secrets = append(secrets, v)
		}
	}
	return hosts, secrets, reason, nil
}

// RequestTool lets me ask my owner to let the live organism reach hosts or
// read secrets. Every new grant needs the owner's approval.
func (e *Egress) RequestTool() *tools.Tool {
	return &tools.Tool{
		Name: "request_outbound_access",
		Description: "Ask my owner to let my live organism reach external hosts over HTTPS and/or read secrets they passed at start " +
			"(as environment variables). Use exact host names (api.stripe.com) or *.example.com for subdomains; no IPs, ports or URLs. " +
			"My owner sees exactly this request and approves or denies it.",
		Schema: tools.Schema(tools.Props{
			"hosts":   tools.StrList("host names the organism needs to reach over HTTPS"),
			"secrets": tools.StrList("secret names (environment variables) the organism needs, e.g. STRIPE_SECRET_KEY"),
			"reason":  tools.Str("what the organism uses them for, in a sentence my owner will read"),
		}, "reason"),
		// Every request is the owner's to decide: an earlier approval (of
		// access they may have revoked since) is never reused.
		AskEveryTime: true,
		Classify: func(input json.RawMessage) (permissions.Level, string) {
			hosts, secrets, _, err := e.parse(input)
			if err != nil {
				return permissions.Safe, "check outbound access" // Run reports the problem; nothing is granted
			}
			var parts []string
			if len(hosts) > 0 {
				parts = append(parts, "reach "+strings.Join(hosts, ", "))
			}
			if len(secrets) > 0 {
				parts = append(parts, "read the secrets "+strings.Join(secrets, ", "))
			}
			return permissions.Dangerous, "let my live organism " + strings.Join(parts, " and ")
		},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			hosts, secrets, reason, err := e.parse(input)
			if err != nil {
				return "", err
			}
			if err := e.Grant(ctx, hosts, secrets, reason); err != nil {
				return "", err
			}
			msg := "My owner approved."
			if len(hosts) > 0 {
				msg += " The live organism can now reach " + strings.Join(hosts, ", ") + " over HTTPS."
			}
			if len(secrets) > 0 {
				msg += " It will find " + strings.Join(secrets, ", ") + " in its environment (only if passed at start; never during tests)."
			}
			return msg, nil
		},
	}
}
