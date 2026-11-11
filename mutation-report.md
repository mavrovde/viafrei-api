## Mutation testing, pr mode

generated 96, killed 82, survived 4, timed out 0, equivalent 0, not covered 10, not viable 0.

Score 85.4% (killed ÷ (generated − equivalent)), minimum 80%.

| Not detected | Mutator | Status |
|---|---|---|
| internal/domain/roads/roads.go:240:20 | INVERT_LOGICAL | NOT COVERED |
| internal/domain/roads/roads.go:240:13 | CONDITIONALS_NEGATION | NOT COVERED |
| internal/domain/roads/roads.go:240:30 | CONDITIONALS_NEGATION | NOT COVERED |
| internal/domain/roads/roads.go:242:20 | INVERT_LOGICAL | NOT COVERED |
| internal/domain/roads/roads.go:242:13 | CONDITIONALS_NEGATION | NOT COVERED |
| internal/domain/roads/roads.go:242:36 | CONDITIONALS_BOUNDARY | NOT COVERED |
| internal/domain/roads/roads.go:242:36 | CONDITIONALS_NEGATION | NOT COVERED |
| internal/domain/roads/roads.go:234:31 | CONDITIONALS_BOUNDARY | LIVED |
| internal/adapters/httpapi/auth.go:181:115 | ARITHMETIC_BASE | NOT COVERED |
| internal/adapters/httpapi/auth.go:181:98 | ARITHMETIC_BASE | NOT COVERED |
| internal/adapters/httpapi/auth.go:215:5 | INVERT_LOOPCTRL | LIVED |
| internal/adapters/httpapi/limits.go:230:79 | CONDITIONALS_NEGATION | NOT COVERED |
| internal/adapters/httpapi/limits.go:190:17 | CONDITIONALS_BOUNDARY | LIVED |
| internal/adapters/httpapi/limits.go:194:19 | CONDITIONALS_BOUNDARY | LIVED |
