#!/usr/bin/env bash
# Copyright (c) 2026 Tencent Inc.
# SPDX-License-Identifier: Apache-2.0
#
# Lightweight build + unit-test gate for self-hosted CI.
#
# The gate builds the primary binaries and runs the primary component unit
# tests. Extended, integration, and path-selected tests are intentionally out
# of scope.

set -euo pipefail

usage() {
	cat <<'EOF'
Run the lightweight CubeSandbox build and unit-test gate.

Usage:
  scripts/run-build-unit-gate.sh [options]

Options:
  --skip-build                  Skip primary binary builds.
  --skip-tests                  Skip unit tests.
  -h, --help                    Show this help.

Environment:
  BUILDER_IMAGE                 Builder image name.
                                Default: cube-sandbox-builder:ubuntu2004
  BUILDER_HOME                  Persistent builder/Cargo cache directory.
EOF
}

find_repo_root() {
	local source_path="${BASH_SOURCE[0]}"
	local current_dir

	while [[ -L "${source_path}" ]]; do
		local link_target
		link_target="$(readlink "${source_path}")"
		if [[ "${link_target}" == /* ]]; then
			source_path="${link_target}"
		else
			source_path="$(dirname "${source_path}")/${link_target}"
		fi
	done

	current_dir="$(cd "$(dirname "${source_path}")" && pwd)"
	while [[ "${current_dir}" != "/" ]]; do
		if [[ -f "${current_dir}/Makefile" ]] && grep -qE '^builder-run:' "${current_dir}/Makefile"; then
			printf '%s\n' "${current_dir}"
			return 0
		fi
		current_dir="$(dirname "${current_dir}")"
	done
	return 1
}

gate_skip_build=0
gate_skip_tests=0

while [[ $# -gt 0 ]]; do
	case "$1" in
	--skip-build)
		gate_skip_build=1
		shift
		;;
	--skip-tests)
		gate_skip_tests=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "error: unknown option: $1" >&2
		usage >&2
		exit 2
		;;
	esac
done

repo_root="$(find_repo_root)" || {
	echo "error: could not locate the CubeSandbox repository root" >&2
	exit 2
}
cd "${repo_root}"

for required_command in make docker; do
	if ! command -v "${required_command}" >/dev/null 2>&1; then
		echo "error: required command not found: ${required_command}" >&2
		exit 2
	fi
done

export BUILDER_IMAGE="${BUILDER_IMAGE:-cube-sandbox-builder:ubuntu2004}"

section() {
	printf '\n===== %s =====\n' "$1"
}

run_make_target() {
	local target="$1"
	echo ">>> make ${target}"
	make "${target}" BUILDER_IMAGE="${BUILDER_IMAGE}"
}

printf 'Repository:      %s\n' "${repo_root}"
printf 'Builder image:  %s\n' "${BUILDER_IMAGE}"

if [[ "${gate_skip_build}" != "1" ]]; then
	section "Primary binary builds"
	primary_build_targets=(
		cubeapi
		cubeops
		cubemaster
		cubetemplatecenter
		cubelet
		agent
		shim
		cube-volume-s3
	)
	for target in "${primary_build_targets[@]}"; do
		run_make_target "${target}"
	done

	section "Verify build outputs"
	expected_binaries=(
		_output/bin/cube-api
		_output/bin/cubeops
		_output/bin/cubeopscli
		_output/bin/cubemaster
		_output/bin/cubemastercli
		_output/bin/templatecenter
		_output/bin/cubelet
		_output/bin/cubecli
		_output/bin/cube-agent
		_output/bin/containerd-shim-cube-rs
		_output/bin/cube-runtime
		_output/bin/cube-volume-s3
	)
	for binary in "${expected_binaries[@]}"; do
		if [[ ! -x "${binary}" ]]; then
			echo "error: expected binary is missing or not executable: ${binary}" >&2
			exit 1
		fi
		echo "PASS ${binary}"
	done
fi

if [[ "${gate_skip_tests}" != "1" ]]; then
	section "Primary component unit tests"
	primary_test_targets=(
		cube-api-test
		cubeops-test
		cubemaster-test
		cubetemplatecenter-test
		shim-test
		cubecow-test-native
		cube-lifecycle-manager-test
		cubelet-test
		agent-test
		hypervisor-test
	)
	for target in "${primary_test_targets[@]}"; do
		run_make_target "${target}"
	done
fi

section "Gate summary"
echo "PASS build and unit-test gate"
