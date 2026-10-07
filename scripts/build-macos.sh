#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$project_root"

# Local builds run the full test gate; release jobs use the same opt-in as Windows.
gradle_args=(clean)
if [[ "${SILVERWING_RELEASE_SKIP_TESTS:-}" != "true" ]]; then
  gradle_args+=(test)
fi
gradle_args+=(:desktop:compileKotlin :desktop:packageDmg --no-daemon)
if [[ "${SILVERWING_RELEASE_SKIP_UNSTABLE_GIT_TESTS:-}" == "true" ]]; then
  gradle_args+=("-PskipHostedGitIntegrationTests")
fi
./gradlew "${gradle_args[@]}"
