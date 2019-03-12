package trace

import (
	"context"
	"testing"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metadata"
	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/pkg/rcode"
	"github.com/coredns/coredns/plugin/test"
	"github.com/coredns/coredns/request"

	"github.com/mholt/caddy"
	"github.com/miekg/dns"
	"github.com/opentracing/opentracing-go/mocktracer"
)

const (
	server                  = "coolServer"
	staticTagName           = "staticTag"
	staticTagValue          = "staticValue"
	dynamicTagName          = "dynamicTag"
	dynamicTagValue         = "{test/dynamicValue}"
	dynamicTagResolvedValue = "resolvedValue"
	invalidDynamicTagName   = "invalidDynamicTag"
	invalidDynamicTagValue  = "{test/doesnotexist}"
)

func TestStartup(t *testing.T) {
	m, err := traceParse(caddy.NewTestController("dns", `trace`))
	if err != nil {
		t.Errorf("Error parsing test input: %s", err)
		return
	}
	if m.Name() != "trace" {
		t.Errorf("Wrong name from GetName: %s", m.Name())
	}
	err = m.OnStartup()
	if err != nil {
		t.Errorf("Error starting tracing plugin: %s", err)
		return
	}
	if m.Tracer() == nil {
		t.Errorf("Error, no tracer created")
	}
}

type testProvider map[string]metadata.Func

func (tp testProvider) Metadata(ctx context.Context, state request.Request) context.Context {
	for k, v := range tp {
		metadata.SetValueFunc(ctx, k, v)
	}
	return ctx
}

func TestTrace(t *testing.T) {
	cases := []struct {
		name     string
		rcode    int
		question *dns.Msg
		server   string
		tags     map[string]string
	}{
		{
			name:     "NXDOMAIN",
			rcode:    dns.RcodeNameError,
			question: new(dns.Msg).SetQuestion("example.org.", dns.TypeA),
			tags: map[string]string{
				staticTagName:         staticTagValue,
				dynamicTagName:        dynamicTagValue,
				invalidDynamicTagName: invalidDynamicTagValue,
			},
		},
		{
			name:     "NOERROR",
			rcode:    dns.RcodeSuccess,
			question: new(dns.Msg).SetQuestion("example.net.", dns.TypeCNAME),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := dnstest.NewRecorder(&test.ResponseWriter{})
			m := mocktracer.New()
			tr := &trace{
				Next: test.HandlerFunc(func(_ context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
					m := new(dns.Msg)
					m.SetRcode(r, tc.rcode)
					w.WriteMsg(m)
					return tc.rcode, nil
				}),
				every:  1,
				tracer: m,
				tags:   tc.tags,
			}

			ctx := context.WithValue(context.TODO(), plugin.ServerCtx{}, server)

			expectedMetadata := []metadata.Provider{
				testProvider{dynamicTagValue[1 : len(dynamicTagValue)-1]: func() string { return dynamicTagResolvedValue }},
			}

			meta := metadata.Metadata{
				Zones:     []string{"."},
				Providers: expectedMetadata,
				Next:      tr,
			}

			if _, err := meta.ServeDNS(ctx, w, tc.question); err != nil {
				t.Fatalf("Error during tr.ServeDNS(ctx, w, %v): %v", tc.question, err)
			}

			fs := m.FinishedSpans()
			// Each trace consists of two spans; the root and the Next function.
			if len(fs) != 2 {
				t.Fatalf("Unexpected span count: len(fs): want 2, got %v", len(fs))
			}

			rootSpan := fs[1]
			req := request.Request{W: w, Req: tc.question}
			if rootSpan.OperationName != spanName(ctx, req) {
				t.Errorf("Unexpected span name: rootSpan.Name: want %v, got %v", spanName(ctx, req), rootSpan.OperationName)
			}
			if rootSpan.Tag(tagName) != req.Name() {
				t.Errorf("Unexpected span tag: rootSpan.Tag(%v): want %v, got %v", tagName, req.Name(), rootSpan.Tag(tagName))
			}
			if rootSpan.Tag(tagType) != req.Type() {
				t.Errorf("Unexpected span tag: rootSpan.Tag(%v): want %v, got %v", tagType, req.Type(), rootSpan.Tag(tagType))
			}
			if rootSpan.Tag(tagRcode) != rcode.ToString(tc.rcode) {
				t.Errorf("Unexpected span tag: rootSpan.Tag(%v): want %v, got %v", tagRcode, rcode.ToString(tc.rcode), rootSpan.Tag(tagRcode))
			}
			if len(tc.tags) == 0 {
				return
			}
			if rootSpan.Tag(staticTagName) != staticTagValue {
				t.Errorf("Unexpected span tag: rootSpan.Tag(%v): want %v, got %v", staticTagName, staticTagValue, rootSpan.Tag(staticTagName))
			}
			if rootSpan.Tag(dynamicTagName) != dynamicTagResolvedValue {
				t.Errorf("Unexpected span tag: rootSpan.Tag(%v): want %v, got %v", dynamicTagName, dynamicTagResolvedValue, rootSpan.Tag(dynamicTagName))
			}
			if rootSpan.Tag(invalidDynamicTagName) != nil {
				t.Errorf("Unexpected span tag: rootSpan.Tag(%v): wanted nil, got %v", invalidDynamicTagName, rootSpan.Tag(invalidDynamicTagName))
			}
		})
	}
}
