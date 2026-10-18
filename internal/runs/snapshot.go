package runs

import (
	"mirrordock/internal/config"
	"net/url"
)

// publicSource is the source as stored in a run snapshot. A custom rsync proxy
// is read again at execution and may carry credentials, so it is dropped.
func publicSource(source config.Source) config.Source {
	source.Rsync.Proxy = ""
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
