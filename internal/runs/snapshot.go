package runs

import (
	"cmp"
	"encoding/json/v2"
	"mirrordock/internal/config"
	"net/url"
	"slices"
	"strings"
)

func publicSource(source config.Source) config.Source {
	source.Upstreams = cloneAddresses(source.Upstreams)
	source.RepoSync.GPGKeys = append([]string(nil), source.RepoSync.GPGKeys...)
	for i, address := range source.RepoSync.GPGKeys {
		source.RepoSync.GPGKeys[i] = publicAddress(address)
	}
	return source
}

func cloneAddresses(addresses map[string]string) map[string]string {
	out := make(map[string]string, len(addresses))
	for key, address := range addresses {
		out[key] = publicAddress(address)
	}
	return out
}

func publicAddress(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return "[REDACTED]"
	}
	if u.User == nil && u.RawQuery == "" && u.Fragment == "" && !u.ForceQuery {
		return address
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
	return u.String()
}

func redactHistoricalURLs(data []byte) ([]byte, error) {
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	replacements := map[string]string{}
	for _, address := range r.Snapshot.Source.Upstreams {
		if clean := publicAddress(address); clean != address {
			replacements[address] = clean
		}
	}
	for _, address := range r.Snapshot.Source.RepoSync.GPGKeys {
		if clean := publicAddress(address); clean != address {
			replacements[address] = clean
		}
	}
	for _, attempt := range r.Attempts {
		if clean := publicAddress(attempt.Candidate); clean != attempt.Candidate {
			replacements[attempt.Candidate] = clean
		}
	}
	if len(replacements) == 0 {
		return data, nil
	}
	r.Snapshot.Source = publicSource(r.Snapshot.Source)
	addresses := make([]string, 0, len(replacements))
	for address := range replacements {
		addresses = append(addresses, address)
	}
	// Replace longer addresses first: one signed URL may prefix another.
	slices.SortFunc(addresses, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	cleanText := func(text string) string {
		for _, address := range addresses {
			text = strings.ReplaceAll(text, address, replacements[address])
		}
		return text
	}
	for i := range r.Attempts {
		r.Attempts[i].Candidate = publicAddress(r.Attempts[i].Candidate)
		r.Attempts[i].Error = cleanText(r.Attempts[i].Error)
	}
	for i := range r.Events {
		r.Events[i].Message = cleanText(r.Events[i].Message)
	}
	r.Error = cleanText(r.Error)
	return json.Marshal(r)
}
