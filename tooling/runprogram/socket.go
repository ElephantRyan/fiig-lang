package runprogram

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"fiig-lang/compiler/runtime"
)

// SocketScriptFactory builds a scripted Socket (ADR 0060 §6) from a -caps
// entry:
//
//	{"Socket": {"type": "socket-script", "peers": [
//	    {"host": "db", "port": 5432, "steps": [
//	        {"expectHex": "0000000804d2162f"},
//	        {"send": "S"},
//	        {"startTls": true},
//	        {"hangup": true}
//	    ]}
//	]}}
//
// expect/send take text; expectHex/sendHex take hex for binary protocols.
// A peer may instead carry "dialError": "Refused" (any SocketError variant).
func SocketScriptFactory(config map[string]any) (runtime.CapabilityProvider, error) {
	raw, err := json.Marshal(config["peers"])
	if err != nil {
		return nil, err
	}
	var peers []struct {
		Host      string `json:"host"`
		Port      int    `json:"port"`
		DialError string `json:"dialError"`
		Steps     []struct {
			Expect    *string `json:"expect"`
			ExpectHex *string `json:"expectHex"`
			Send      *string `json:"send"`
			SendHex   *string `json:"sendHex"`
			StartTLS  bool    `json:"startTls"`
			TLSError  string  `json:"tlsError"`
			Hangup    bool    `json:"hangup"`
			Stall     bool    `json:"stall"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &peers); err != nil {
		return nil, fmt.Errorf("peers: %w", err)
	}
	script := &runtime.ScriptedSocket{}
	for i, p := range peers {
		peer := &runtime.SocketPeer{Host: p.Host, Port: p.Port}
		if p.DialError != "" {
			peer.DialError = &runtime.SocketFailure{Variant: p.DialError}
		}
		for j, st := range p.Steps {
			step := runtime.SocketStep{StartTLS: st.StartTLS, TLSError: st.TLSError, Hangup: st.Hangup, Stall: st.Stall}
			var err error
			if step.Expect, err = textOrHex(st.Expect, st.ExpectHex); err != nil {
				return nil, fmt.Errorf("peers[%d].steps[%d].expectHex: %w", i, j, err)
			}
			if step.Send, err = textOrHex(st.Send, st.SendHex); err != nil {
				return nil, fmt.Errorf("peers[%d].steps[%d].sendHex: %w", i, j, err)
			}
			peer.Steps = append(peer.Steps, step)
		}
		script.Peers = append(script.Peers, peer)
	}
	return runtime.SocketProvider{Backend: script}, nil
}

// SocketFactory builds the real Socket; it takes no configuration yet (host
// allowlists and custom CAs are ADR 0060's open questions 1 and 2).
func SocketFactory(map[string]any) (runtime.CapabilityProvider, error) {
	return runtime.NewSocketProvider(), nil
}

func textOrHex(text, hexText *string) ([]byte, error) {
	switch {
	case text != nil:
		return []byte(*text), nil
	case hexText != nil:
		return hex.DecodeString(*hexText)
	}
	return nil, nil
}
