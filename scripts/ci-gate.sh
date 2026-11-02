#!/usr/bin/env bash
# ci-gate.sh SHA
#
# Refuses unless the required CI checks completed with success on commit SHA
# (REL-015). Run before a tag is created and before any image is pushed, so a
# tag or an image never exists for a commit CI has not passed. A check still
# queued or running is a refusal, not a wait.
#
# Needs GH_TOKEN (checks: read) and GH_REPO. The required names are the job
# names in .github/workflows/ci.yml; TestReleaseGateIsWired holds them equal.
set -euo pipefail

sha=${1:?usage: ci-gate.sh SHA}
: "${GH_REPO:?GH_REPO must name the repository (owner/name)}"
: "${GH_TOKEN:?GH_TOKEN must be set (the job needs checks: read)}"

answer=$(gh api --paginate --slurp "repos/$GH_REPO/commits/$sha/check-runs?per_page=100")
go run ./internal/release/cigate -sha "$sha" \
  -require "Unit and handler tests" \
  -require "Integration tests (PostGIS)" \
  -require "Coverage gate (100 % per package)" <<<"$answer"
