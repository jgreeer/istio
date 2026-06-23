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

package model

import (
	"fmt"
	"testing"

	networking "istio.io/api/networking/v1alpha3"

	"istio.io/istio/pkg/config"
)

// gatewayParentAnnotation is hardcoded (rather than referencing constants.InternalGatewayParent)
// so this benchmark compiles unchanged against both the local build (with the misdirected-requests
// change) and stock master (where the constant and computeMisdirectedHosts do not exist). On master
// the annotation is inert; on the local build it triggers the misdirected-hosts computation in
// mergeGateways. This lets us time the worst-case push impact by running here, `git stash`, run again.
const gatewayParentAnnotation = "internal.istio.io/gateway-parent"

// makeMisdirectedGateway builds a single-server HTTPS Gateway on port 443 that all share the same
// logical parent, so they bucket together as siblings in computeMisdirectedHosts.
func makeMisdirectedGateway(i int, host string) config.Config {
	c := makeConfig(fmt.Sprintf("gw-%d", i), "test-ns", host, "https", "HTTPS", 443, "ingressgateway", "",
		networking.ServerTLSSettings_SIMPLE, []string{}, "sa")
	c.Annotations[gatewayParentAnnotation] = "test-ns/parent"
	return c
}

// buildMisdirectedInstances creates N HTTPS listeners on the same port/parent.
// When catchAll is true, one listener is a "*" catch-all (output stays O(N) on the local build but
// compute is still O(N^2)); when false, every host is distinct (both compute and emitted config are
// O(N^2) on the local build — the true worst case).
func buildMisdirectedInstances(n int, catchAll bool) []gatewayWithInstances {
	instances := make([]gatewayWithInstances, 0, n)
	start := 0
	if catchAll {
		instances = append(instances, gatewayWithInstances{makeMisdirectedGateway(0, "*"), true, nil})
		start = 1
	}
	for i := start; i < n; i++ {
		instances = append(instances, gatewayWithInstances{makeMisdirectedGateway(i, fmt.Sprintf("host-%d.example.com", i)), true, nil})
	}
	return instances
}

func BenchmarkMergeGatewaysMisdirected(b *testing.B) {
	sizes := []int{100, 500, 1000, 2000, 5000}
	proxy := &Proxy{}
	for _, n := range sizes {
		for _, catchAll := range []bool{true, false} {
			name := "nocatchall"
			if catchAll {
				name = "catchall"
			}
			instances := buildMisdirectedInstances(n, catchAll)
			b.Run(fmt.Sprintf("%s/N=%d", name, n), func(b *testing.B) {
				pc := makePushContext()
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					_ = mergeGateways(instances, proxy, pc)
				}
			})
		}
	}
}
