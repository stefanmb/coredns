package trace

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metadata"

	"github.com/mholt/caddy"
)

func init() {
	caddy.RegisterPlugin("trace", caddy.Plugin{
		ServerType: "dns",
		Action:     setup,
	})
}

func setup(c *caddy.Controller) error {
	t, err := traceParse(c)
	if err != nil {
		return plugin.Error("trace", err)
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		t.Next = next
		return t
	})

	c.OnStartup(t.OnStartup)

	return nil
}

func traceParse(c *caddy.Controller) (*trace, error) {
	var (
		tr  = &trace{every: 1, serviceName: defServiceName}
		err error
	)

	cfg := dnsserver.GetConfig(c)
	tr.serviceEndpoint = cfg.ListenHosts[0] + ":" + cfg.Port

	for c.Next() { // trace
		var err error
		args := c.RemainingArgs()
		switch len(args) {
		case 0:
			tr.EndpointType, tr.Endpoint, err = normalizeEndpoint(defEpType, "")
		case 1:
			tr.EndpointType, tr.Endpoint, err = normalizeEndpoint(defEpType, args[0])
		case 2:
			epType := strings.ToLower(args[0])
			tr.EndpointType, tr.Endpoint, err = normalizeEndpoint(epType, args[1])
		default:
			err = c.ArgErr()
		}
		if err != nil {
			return tr, err
		}
		for c.NextBlock() {
			switch c.Val() {
			case "every":
				args := c.RemainingArgs()
				if len(args) != 1 {
					return nil, c.ArgErr()
				}
				tr.every, err = strconv.ParseUint(args[0], 10, 64)
				if err != nil {
					return nil, err
				}
			case "service":
				args := c.RemainingArgs()
				if len(args) != 1 {
					return nil, c.ArgErr()
				}
				tr.serviceName = args[0]
			case "client_server":
				args := c.RemainingArgs()
				if len(args) > 1 {
					return nil, c.ArgErr()
				}
				tr.clientServer = true
				if len(args) == 1 {
					tr.clientServer, err = strconv.ParseBool(args[0])
				}
				if err != nil {
					return nil, err
				}
			case "tag":
				args := c.RemainingArgs()
				if len(args) != 2 {
					return nil, c.ArgErr()
				}
				if tr.tags == nil {
					tr.tags = map[string]string{}
				}
				if tr.tagFetchers == nil {
					tr.tagFetchers = map[string]metadata.Func{}
				}

				internalKey, fetcher := tagFetcher(tr, args[0], args[1])
				if fetcher != nil {
					tr.tagFetchers[internalKey] = fetcher
				}
				tr.tags[args[0]] = internalKey
			}
		}
	}
	return tr, err
}

func tagFetcher(tr *trace, key string, value string) (string, metadata.Func) {
	if len(value) > 2 && value[0] == '{' && value[len(value)-1] == '}' {
		// We cannot get the fetcher at setup time, it has to be
		// obtained dynamically at request time, since it may be
		// added after the initial setup and we need a context.
		return value[1 : len(value)-1], nil
	}
	return fmt.Sprintf("%s/%s", tr.Name(), key), func() string {
		return value
	}
}

func normalizeEndpoint(epType, ep string) (string, string, error) {
	if _, ok := supportedProviders[epType]; !ok {
		return "", "", fmt.Errorf("tracing endpoint type '%s' is not supported", epType)
	}

	if ep == "" {
		ep = supportedProviders[epType]
	}

	if epType == "zipkin" {
		if !strings.Contains(ep, "http") {
			ep = "http://" + ep + "/api/v1/spans"
		}
	}

	return epType, ep, nil
}

var supportedProviders = map[string]string{
	"zipkin":  "localhost:9411",
	"datadog": "localhost:8126",
}

const (
	defEpType      = "zipkin"
	defServiceName = "coredns"
)
