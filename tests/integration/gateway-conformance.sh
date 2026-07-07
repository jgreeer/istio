#!/bin/bash

# Copyright Istio Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Run the upstream Gateway API conformance suite against Istio in a local
# kind cluster. A kind cluster is created (unless --skip-setup is supplied)
# and MetalLB is installed to provide LoadBalancer addresses for Gateway
# resources. The data plane mode (sidecar, ambient, or agentgateway) is
# selectable via --mode.
#
# This script is a thin wrapper around prow/integ-suite-kind.sh: it sets the
# correct integration target and TEST_SELECT/-run filters so that only the
# Gateway API conformance tests execute, and ensures MetalLB is installed.

set -euo pipefail

WD=$(dirname "$0")
WD=$(cd "$WD"; pwd)
ROOT=$(dirname "$(dirname "$WD")")

MODE="sidecar"
CLUSTER_NAME_DEFAULT="gwapi-conformance"
NODE_IMAGE_DEFAULT="registry.istio.io/testing/kind-node:v1.35.0"

SKIP_BUILD=""
SKIP_SETUP=""
SKIP_CLEANUP=""
TESTS=""

usage() {
  cat <<EOF
Usage: $(basename "$0") [options]

Run the Gateway API conformance tests for Istio in a local kind cluster.
A kind cluster is created and MetalLB is installed automatically so that
Gateway resources receive LoadBalancer addresses.

Options:
  --mode {sidecar|ambient|agentgateway}
                             Data plane mode under test (default: sidecar).
                             'agentgateway' runs TestGatewayConformanceAgentgateway
                             against the istio-agentgateway GatewayClass.
  --agentgateway             Shorthand for --mode agentgateway.
  --tests <list>             Comma-separated list of conformance test ShortNames
                             to run (e.g. HTTPRouteSimpleSameNamespace).
                             May be repeated. All other conformance tests are
                             skipped. Names must match upstream ShortName values.
  --cluster-name <name>      kind cluster name (default: ${CLUSTER_NAME_DEFAULT}).
                             May also be set via the CLUSTER_NAME env var.
  --node-image <image>       kind node image (default: ${NODE_IMAGE_DEFAULT}).
                             May also be set via the NODE_IMAGE env var.
  --skip-setup               Re-use an existing kind cluster (skip kind +
                             MetalLB setup).
  --skip-build               Re-use already-built/pushed Istio images.
  --skip-cleanup             Leave the cluster running after the tests.
  -h, --help                 Show this help.

Environment overrides:
  HUB, TAG, VARIANT          Forwarded to the underlying build/test.
  INTEGRATION_TEST_FLAGS     Extra flags appended to the go test invocation.
  ARTIFACTS                  Directory for test artifacts (auto-created).

Examples:
  # Run the sidecar conformance tests on a fresh kind cluster.
  $(basename "$0") --mode sidecar

  # Run the ambient conformance tests, keep the cluster around afterwards.
  $(basename "$0") --mode ambient --skip-cleanup

  # Run the agentgateway conformance tests on a fresh kind cluster.
  $(basename "$0") --mode agentgateway

  # Re-run against an already-prepared cluster + images.
  $(basename "$0") --mode ambient --skip-setup --skip-build

  # Run only specific conformance tests.
  $(basename "$0") --mode sidecar \\
    --tests HTTPRouteSimpleSameNamespace,HTTPRouteHostnameIntersection
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)
      MODE="${2:-}"
      shift 2
      ;;
    --mode=*)
      MODE="${1#*=}"
      shift
      ;;
    --agentgateway)
      MODE="agentgateway"
      shift
      ;;
    --tests)
      if [[ -z "${TESTS}" ]]; then
        TESTS="${2:-}"
      else
        TESTS="${TESTS},${2:-}"
      fi
      shift 2
      ;;
    --tests=*)
      val="${1#*=}"
      if [[ -z "${TESTS}" ]]; then
        TESTS="${val}"
      else
        TESTS="${TESTS},${val}"
      fi
      shift
      ;;
    --cluster-name)
      CLUSTER_NAME="${2:-}"
      shift 2
      ;;
    --cluster-name=*)
      CLUSTER_NAME="${1#*=}"
      shift
      ;;
    --node-image)
      NODE_IMAGE="${2:-}"
      shift 2
      ;;
    --node-image=*)
      NODE_IMAGE="${1#*=}"
      shift
      ;;
    --skip-setup)
      SKIP_SETUP="true"
      shift
      ;;
    --skip-build)
      SKIP_BUILD="true"
      shift
      ;;
    --skip-cleanup)
      SKIP_CLEANUP="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

case "${MODE}" in
  sidecar)
    INTEG_TARGET="test.integration.pilot.kube"
    ;;
  ambient)
    INTEG_TARGET="test.integration.ambient.kube"
    ;;
  agentgateway)
    # TestGatewayConformanceAgentgateway lives in its own package with a
    # dedicated TestMain that installs Istio with PILOT_ENABLE_AGENTGATEWAY=true.
    # It is a distinct package from the sidecar pilot tests, so it needs its own
    # integration target rather than sharing the pilot target.
    INTEG_TARGET="test.integration.pilot.agentgateway.kube"
    ;;
  *)
    echo "Invalid --mode '${MODE}'. Must be 'sidecar', 'ambient', or 'agentgateway'." >&2
    exit 1
    ;;
esac

# Build the -run regex for go test. The integration framework still runs the
# package's TestMain (control-plane install, echo deployments, etc.), but only
# the Gateway API conformance test functions will be executed.
#
# IMPORTANT: integ-suite-kind.sh shells out to `make`, and the integration
# Makefile assigns INTEGRATION_TEST_FLAGS via `:=`. Make would interpret a
# literal `$` inside the value as the start of a Make variable reference (so
# `^TestGatewayConformance$ --foo` collapses to `^TestGatewayConformance--foo`
# and matches no tests). Use `$$` here so make emits a single `$` to the shell.
if [[ "${MODE}" == "agentgateway" ]]; then
  RUN_REGEX='^TestGatewayConformanceAgentgateway$$'
else
  RUN_REGEX='^TestGatewayConformance$$'
fi

export CLUSTER_NAME="${CLUSTER_NAME:-${CLUSTER_NAME_DEFAULT}}"
export NODE_IMAGE="${NODE_IMAGE:-${NODE_IMAGE_DEFAULT}}"

# Ensure MetalLB is installed by the kind provisioner. This is the default
# behaviour of common/scripts/kind_provisioner.sh, but make it explicit so a
# stray export in the caller's environment does not silently disable it.
unset NOMETALBINSTALL || true
export TEST_ENV=kind-metallb

# Confine the test run to the Gateway API conformance tests. SINGLE_PACKAGE
# avoids descending into sub-packages of pilot/ambient that would expand the
# test set.
export INTEGRATION_TEST_FLAGS="${INTEGRATION_TEST_FLAGS:-} -run=${RUN_REGEX}"

# The agentgateway conformance test is gated behind an explicit opt-in flag
# because agentgateway support is still experimental. Without this flag the test
# skips itself (see ctx.Settings().Agentgateway).
if [[ "${MODE}" == "agentgateway" ]]; then
  INTEGRATION_TEST_FLAGS="${INTEGRATION_TEST_FLAGS} --istio.test.agentgateway"
fi

# Restrict the conformance suite to a user-specified subset, if any. Names are
# the upstream ConformanceTest ShortName values.
if [[ -n "${TESTS}" ]]; then
  INTEGRATION_TEST_FLAGS="${INTEGRATION_TEST_FLAGS} --istio.test.gatewayConformanceTests=${TESTS}"
fi

INTEG_FLAGS=(--node-image "${NODE_IMAGE}")
if [[ -n "${SKIP_SETUP}" ]]; then
  INTEG_FLAGS+=(--skip-setup)
fi
if [[ -n "${SKIP_BUILD}" ]]; then
  INTEG_FLAGS+=(--skip-build)
fi
if [[ -n "${SKIP_CLEANUP}" ]]; then
  INTEG_FLAGS+=(--skip-cleanup)
fi

echo "Running Gateway API conformance tests"
echo "  mode:         ${MODE}"
echo "  target:       ${INTEG_TARGET}"
echo "  cluster:      ${CLUSTER_NAME}"
echo "  node image:   ${NODE_IMAGE}"
echo "  -run filter:  ${RUN_REGEX//\$\$/\$}"
if [[ -n "${TESTS}" ]]; then
  echo "  tests:        ${TESTS}"
fi

exec "${ROOT}/prow/integ-suite-kind.sh" \
  "${INTEG_FLAGS[@]}" \
  "${INTEG_TARGET}" \
  SINGLE_PACKAGE=true
