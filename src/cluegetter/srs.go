// ClueGetter - Does things with mail
//
// Copyright 2016 Dolf Schimmel, Freeaqingme.
//
// This Source Code Form is subject to the terms of the two-clause BSD license.
// For its contents, please refer to the LICENSE file.
//
package main

import (
	"fmt"
	"strings"

	"cluegetter/address"
	"regexp"
	"time"
)

var srsMatch = regexp.MustCompile(`^(?i)SRS[0-9]+=`)

func init() {
	enable := func() bool { return Config.Srs.Enabled }
	milterCheck := srsMilterCheck

	ModuleRegister(&module{
		name:        "srs",
		enable:      &enable,
		milterCheck: &milterCheck,
	})
}

func srsMilterCheck(msg *Message, abort chan bool) *MessageCheckResult {
	from := ""
	srsIn := srsGetInboundSrsAddresses(msg)

	if len(srsIn) > 0 && len(msg.Rcpt) > 1 {
		Log.Notice("More than 1 recipient including an SRS recipient, that's weird?")
	}

	var mapped map[string]string
	if len(srsIn) > 0 {
		mapped = srsSwapRecipients(msg, srsIn)
	} else {
		from = srsGetFromAddress(msg)
		milterChangeFrom(msg.session.milterCtx, from)
		go func() {
			cluegetterRecover("srsPersist")
			srsPersist(msg, from)
		}()
	}

	return &MessageCheckResult{
		module:          "srs",
		suggestedAction: messagePermit,
		score:           0,
		determinants: map[string]interface{}{
			"from":         from,
			"is-forwarded": srsIsForwarded(msg),
			"mapped":       mapped,
		},
	}
}

func srsIsValidRecipient(address *address.Address) bool {
	return srsLookupAddress(address) != ""
}

func srsSwapRecipients(msg *Message, srsAddresses []address.Address) map[string]string {
	out := make(map[string]string, 0)
	for _, srsAddress := range srsAddresses {
		out[srsAddress.String()] = srsLookupAddress(&srsAddress)

		milterDelRcpt(msg.session.milterCtx, srsAddress.String())
		milterAddRcpt(msg.session.milterCtx, out[srsAddress.String()])
	}

	return out
}

func srsLookupAddress(address *address.Address) string {
	key := strings.ToLower(fmt.Sprintf("cluegetter--srs-entry-%s", address.String()))
	out, _ := redisClient.Get(key).Result()
	return out
}

// Todo: Also persist in DB?
func srsPersist(msg *Message, from string) {
	key := strings.ToLower(fmt.Sprintf("cluegetter--srs-entry-%s", from))
	redisClient.Set(key, msg.From.String(), 7*24*time.Hour)
}

func srsIsSrsAddress(address *address.Address) bool {
	if !Config.Srs.Enabled {
		return false // If SRS is not enabled, nothing is an SRS address
	}

	return srsMatch.MatchString(address.Local())
}

func srsGetInboundSrsAddresses(msg *Message) []address.Address {
	out := make([]address.Address, 0)
	for _, rcpt := range msg.Rcpt {
		if srsIsSrsAddress(rcpt) {
			out = append(out, *rcpt)
		}
	}

	return out
}

func srsGetFromAddress(msg *Message) string {
	if !Config.Srs.Enabled {
		return ""
	}

	if !srsIsForwarded(msg) {
		return ""
	}

	domain := srsGetRewriteDomain(msg)
	if domain == "" {
		Log.Debug("Could not determine SRS domain for %s", msg.QueueId)
		return ""
	}

	return fmt.Sprintf("SRS0=%s=%s=%s@%s",
		msg.QueueId, msg.From.Domain(), msg.From.Local(), domain)
}

func srsGetRewriteDomain(msg *Message) string {
	domains := make([]string, 0)
	for _, hdr := range msg.Headers {
		if strings.EqualFold(hdr.Key, Config.Srs.Recipient_Header) {
			address := address.FromString(strings.ToLower(hdr.Value))
			domains = append(domains, address.Domain())
		}
	}

	for _, rcpt := range msg.Rcpt {
		rcptDomain := strings.ToLower(rcpt.Domain())
		for k, domain := range domains {
			if rcptDomain == domain {
				domains = append(domains[:k], domains[k+1:]...)
			}
		}
	}

	if len(domains) > 1 {
		Log.Debug("Multiple SRS domains to choose from for message '%s': %s",
			msg.QueueId, domains,
		)
	}

	if len(domains) > 0 {
		return domains[0]
	}

	return ""
}

// Checks if the message was forwarded by comparing the recipient list
// to the Config.Srs.Recipient_Header headers. If a recipient does not show in the
// headers, it's safe to say the message was forwarded
func srsIsForwarded(msg *Message) bool {
	for _, rcpt := range msg.Rcpt {

		match := false
		count := 0
		for _, hdr := range msg.Headers {
			if strings.EqualFold(hdr.Key, Config.Srs.Recipient_Header) {
				count++
				if strings.EqualFold(hdr.Value, rcpt.String()) {

					match = true
					break
				}
			}
		}

		if count == 0 { // No Config.Srs.Recipient_Header headers
			return false
		}

		if !match {
			return true
		}
	}

	return false
}
