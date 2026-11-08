#!/usr/bin/env bash
# zap-sarif.sh ZAP-JSON
#
# Writes the SARIF 2.1.0 form of an OWASP ZAP JSON report to stdout, so the
# DAST findings reach GitHub code scanning beside every other scanner. ZAP
# scans the running service, not a file, so each result is located on the
# OpenAPI document the scan was driven by (api/openapi.json) and names the
# method and URL ZAP reported in its message. Risk high is error, medium is
# warning, low and informational are note.
set -euo pipefail

in=${1:?usage: zap-sarif.sh ZAP-JSON}
[[ -s $in ]] || { echo "FAIL: $in is missing or empty" >&2; exit 2; }

jq '
  def level: if . == "3" then "error" elif . == "2" then "warning" else "note" end;
  [.site[]?.alerts[]?] as $alerts
  | {
      "$schema": "https://json.schemastore.org/sarif-2.1.0.json",
      version: "2.1.0",
      runs: [{
        tool: { driver: {
          name: "OWASP ZAP",
          informationUri: "https://www.zaproxy.org/",
          rules: ($alerts | unique_by(.pluginid) | map({
            id: .pluginid,
            name: (.alert // .name),
            shortDescription: { text: (.alert // .name) },
            helpUri: ("https://www.zaproxy.org/docs/alerts/" + .pluginid + "/")
          }))
        }},
        results: [ $alerts[] | . as $a | ($a.instances // [{}])[] | {
          ruleId: $a.pluginid,
          level: ($a.riskcode | level),
          message: { text: (($a.alert // $a.name) + ": " + (.method // "-") + " " + (.uri // "-")) },
          locations: [{ physicalLocation: {
            artifactLocation: { uri: "api/openapi.json" },
            region: { startLine: 1 }
          }}]
        }]
      }]
    }
' "$in"
