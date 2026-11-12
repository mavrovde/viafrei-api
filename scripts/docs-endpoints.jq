# docs-endpoints.jq: the endpoints page of the documentation portal, as
# Markdown, from api/openapi.json alone. Every operation the document has is
# listed, so a new operation appears here without an edit; its description,
# request sample, answer example and attribution rule come from the document
# (api/examples, embedded by make generate).

# JSON indented by two spaces, members in the document's order.
def pretty($i):
  if type == "object" then
    if length == 0 then "{}"
    else "{\n" + ([to_entries[] | ($i + "  ") + (.key | tojson) + ": " + (.value | pretty($i + "  "))] | join(",\n")) + "\n" + $i + "}" end
  elif type == "array" then
    if length == 0 then "[]"
    else "[\n" + (map(($i + "  ") + pretty($i + "  ")) | join(",\n")) + "\n" + $i + "]" end
  else tojson end;
def methods:["get", "put", "post", "delete", "options", "head", "patch", "trace"];
def cell: tostring | gsub("\\|"; "\\|") | gsub("\n"; " ");
def ops:
  [ .paths | to_entries[] | .key as $path | .value | to_entries[]
    | select(.key as $m | methods | index($m))
    | {method: (.key | ascii_upcase), path: $path, op: .value} ]
  | sort_by(.path, .method);
def keyed: (.op.security // []) | length > 0;
def access:
  if keyed then "key" + (.op["x-required-scope"] // null | if . then ", scope `\(.)`" else "" end)
  else "open" end;
def stability: .op["x-stability"] // "not marked";
def anchor: .op.operationId | ascii_downcase;
def success:
  [ .op.responses | to_entries[] | select(.key | test("^2"))
    | {status: .key, ex: (.value.content["application/json"].examples // {} | to_entries[0].value)}
    | select(.ex != null) ][0];

"# Endpoints",
"",
"Every operation of the \(.info.title) \(.info.version) interface, generated from",
"the [OpenAPI document](openapi.json). Each request and answer below was",
"recorded from the service on synthetic fixtures: every name and value in",
"them is fictional ([examples](examples.md)). The [API reference](reference.html)",
"has the full schemas. The samples name the planned host, which is not",
"deployed yet: until it is, send them to your own instance",
"([getting started](getting-started.md)).",
"",
"| Operation | Request | Access | Stability |",
"|---|---|---|---|",
( ops[] | "| [\(.op.operationId)](#\(anchor)) | `\(.method) \(.path)` | \(access) | \(stability) |" ),
"",
"Keyed operations take the key in the `Authorization: Bearer` header",
"([getting started](getting-started.md)). Stability markers are described in the",
"[versioning policy](versioning.md).",
( ops[]
  | . as $o
  | "",
    "## \(.op.operationId)",
    "",
    "`\(.method) \(.path)`: \(.op.summary // "")",
    "",
    (.op.description // ""),
    "",
    "- Access: \(access)",
    "- Stability: \(stability)",
    "- Attribution: \(.op["x-attribution"] // "not documented")",
    ( if ((.op.parameters // []) | length) > 0 then
        "",
        "| Parameter | In | Required | Description |",
        "|---|---|---|---|",
        ( .op.parameters[] | "| `\(.name)` | \(.in) | \(if .required then "yes" else "no" end) | \(.description // "" | cell) |" )
      else empty end ),
    ( (.op["x-codeSamples"] // [])[] |
        "",
        "Request (\(.label // .lang)):",
        "",
        "```sh",
        .source,
        "```" ),
    ( $o | success | select(. != null) |
        "",
        "Answer, status \(.status)\(if .ex.summary then ": " + .ex.summary else "" end):",
        "",
        "```json",
        (.ex.value | pretty("")),
        "```" )
)
