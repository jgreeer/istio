// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package agentgateway

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	"istio.io/istio/pilot/pkg/config/kube/gatewaycommon"
	"istio.io/istio/pkg/kube/krt"
	"istio.io/istio/pkg/kube/krt/krttest"
	"istio.io/istio/pkg/ptr"
	"istio.io/istio/pkg/test/util/assert"
)

func testRouteContext(t *testing.T, services ...*corev1.Service) RouteContext {
	t.Helper()
	opts := krttest.Options(t)
	grants := gatewaycommon.BuildReferenceGrants(
		gatewaycommon.ReferenceGrantsCollection(
			krt.NewStaticCollection[*gatewayv1beta1.ReferenceGrant](nil, nil, opts.WithName("Grants")...), opts))
	return RouteContext{
		Krt: krt.TestingDummyContext{},
		RouteContextInputs: RouteContextInputs{
			DomainSuffix: "cluster.local",
			Grants:       grants,
			Services:     krt.NewStaticCollection(nil, services, opts.WithName("Services")...),
		},
	}
}

func httpBackendRef(name string, port *gatewayv1.PortNumber) gatewayv1.HTTPBackendRef {
	return gatewayv1.HTTPBackendRef{
		BackendRef: gatewayv1.BackendRef{
			BackendObjectReference: gatewayv1.BackendObjectReference{
				Name: gatewayv1.ObjectName(name),
				Port: port,
			},
		},
	}
}

// TestConvertHTTPRouteToAgwRetainsHostnames ensures a route that fails backend resolution is still
// published with its declared hostnames. A route with no hostnames matches every hostname, which
// would make a single broken route a gateway-wide catch-all and turn 404s into 500s.
func TestConvertHTTPRouteToAgwRetainsHostnames(t *testing.T) {
	cases := []struct {
		name        string
		backendRefs []gatewayv1.HTTPBackendRef
		wantReason  ConfigErrorReason
	}{
		{
			name:        "resolvable backend",
			backendRefs: []gatewayv1.HTTPBackendRef{httpBackendRef("backend", ptr.Of(gatewayv1.PortNumber(80)))},
		},
		{
			// Gracefully dropped: the route is published with an unresolvable backend.
			name:        "unresolvable backend",
			backendRefs: []gatewayv1.HTTPBackendRef{httpBackendRef("does-not-exist", ptr.Of(gatewayv1.PortNumber(80)))},
			wantReason:  InvalidDestinationNotFound,
		},
		{
			// Hard translation error: the route is published with no backends at all.
			name:        "backend missing required port",
			backendRefs: []gatewayv1.HTTPBackendRef{httpBackendRef("backend", nil)},
			wantReason:  ConfigErrorReason(gatewayv1.RouteReasonUnsupportedValue),
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testRouteContext(t, &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "default"},
			})
			obj := &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "default"},
				Spec: gatewayv1.HTTPRouteSpec{
					Hostnames: []gatewayv1.Hostname{"tenant.example.com"},
				},
			}
			rule := gatewayv1.HTTPRouteRule{BackendRefs: tt.backendRefs}

			res, err := ConvertHTTPRouteToAgw(ctx, rule, obj, 0, 0)

			if res == nil {
				t.Fatal("expected a route to be returned")
			}
			assert.Equal(t, res.GetHostnames(), []string{"tenant.example.com"})
			if tt.wantReason == "" {
				assert.Equal(t, err, nil)
				return
			}
			if err == nil || err.error == nil {
				t.Fatalf("expected a config error with reason %q, got %v", tt.wantReason, err)
			}
			assert.Equal(t, err.error.Reason, tt.wantReason)
		})
	}
}

func TestConvertGRPCRouteToAgwRetainsHostnames(t *testing.T) {
	ctx := testRouteContext(t)
	obj := &gatewayv1.GRPCRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "default"},
		Spec: gatewayv1.GRPCRouteSpec{
			Hostnames: []gatewayv1.Hostname{"tenant.example.com"},
		},
	}
	rule := gatewayv1.GRPCRouteRule{BackendRefs: []gatewayv1.GRPCBackendRef{{
		BackendRef: gatewayv1.BackendRef{
			BackendObjectReference: gatewayv1.BackendObjectReference{Name: "backend"},
		},
	}}}

	res, err := ConvertGRPCRouteToAgw(ctx, rule, obj, 0)

	if res == nil {
		t.Fatal("expected a route to be returned")
	}
	assert.Equal(t, res.GetHostnames(), []string{"tenant.example.com"})
	if err == nil || err.error == nil {
		t.Fatalf("expected a config error, got %v", err)
	}
}

func TestConvertTLSRouteToAgwRetainsHostnames(t *testing.T) {
	ctx := testRouteContext(t)
	obj := &gatewayv1.TLSRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "default"},
		Spec: gatewayv1.TLSRouteSpec{
			Hostnames: []gatewayv1.Hostname{"tenant.example.com"},
		},
	}
	rule := gatewayv1.TLSRouteRule{BackendRefs: []gatewayv1.BackendRef{{
		BackendObjectReference: gatewayv1.BackendObjectReference{Name: "backend"},
	}}}

	res, err := ConvertTLSRouteToAgw(ctx, rule, obj, 0)

	if res == nil {
		t.Fatal("expected a route to be returned")
	}
	assert.Equal(t, res.GetHostnames(), []string{"tenant.example.com"})
	if err == nil || err.error == nil {
		t.Fatalf("expected a config error, got %v", err)
	}
}

func TestIsInvalidBackend(t *testing.T) {
	cases := []struct {
		name string
		err  *Condition
		want bool
	}{
		{name: "nil condition", err: nil, want: false},
		{name: "no error set", err: &Condition{reason: string(gatewayv1.RouteReasonBackendNotFound)}, want: false},
		{
			name: "backend not found",
			err:  &Condition{error: &ConfigError{Reason: InvalidDestinationNotFound}},
			want: true,
		},
		{
			name: "ref not permitted",
			err:  &Condition{error: &ConfigError{Reason: InvalidDestinationPermit}},
			want: true,
		},
		{
			name: "invalid kind",
			err:  &Condition{error: &ConfigError{Reason: InvalidDestinationKind}},
			want: true,
		},
		{
			name: "unsupported value",
			err:  &Condition{error: &ConfigError{Reason: ConfigErrorReason(gatewayv1.RouteReasonUnsupportedValue)}},
			want: false,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, isInvalidBackend(tt.err), tt.want)
		})
	}
}

// TestInvalidBackendPreservesWeight asserts that an invalid backendRef is still emitted as a
// backend carrying its weight, rather than being dropped. The Gateway API requires the share of
// traffic destined for an invalid backendRef to receive a 500; dropping it would instead
// redistribute that share to the healthy backends.
func TestInvalidBackendPreservesWeight(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*gatewayv1.HTTPBackendRef)
		wantReason ConfigErrorReason
	}{
		{
			name:       "unsupported kind",
			mutate:     func(b *gatewayv1.HTTPBackendRef) { b.Kind = ptr.Of(gatewayv1.Kind("NotAThing")) },
			wantReason: InvalidDestinationKind,
		},
		{
			name: "cross namespace without a ReferenceGrant",
			mutate: func(b *gatewayv1.HTTPBackendRef) {
				b.Namespace = ptr.Of(gatewayv1.Namespace("other"))
			},
			wantReason: InvalidDestinationPermit,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testRouteContext(t, &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "default"},
			})
			valid := httpBackendRef("backend", ptr.Of(gatewayv1.PortNumber(80)))
			valid.Weight = ptr.Of(int32(70))
			broken := httpBackendRef("backend", ptr.Of(gatewayv1.PortNumber(80)))
			broken.Weight = ptr.Of(int32(30))
			tt.mutate(&broken)

			backends, invalidErr, hardErr := buildAgwHTTPDestination(
				ctx, []gatewayv1.HTTPBackendRef{valid, broken}, "default")

			assert.Equal(t, hardErr, nil)
			if invalidErr == nil || invalidErr.error == nil {
				t.Fatalf("expected an invalid-backend condition, got %v", invalidErr)
			}
			assert.Equal(t, invalidErr.error.Reason, tt.wantReason)

			if len(backends) != 2 {
				t.Fatalf("got %d backends, want 2 (the invalid one must keep its weight share)", len(backends))
			}
			assert.Equal(t, backends[0].GetWeight(), int32(70))
			if backends[0].GetBackend() == nil {
				t.Fatal("valid backend should resolve to a backend reference")
			}
			assert.Equal(t, backends[1].GetWeight(), int32(30))
			if backends[1].GetBackend() != nil {
				t.Fatalf("invalid backend should not resolve, got %v", backends[1].GetBackend())
			}
		})
	}
}
