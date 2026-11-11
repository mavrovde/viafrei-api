# Versioning and deprecation policy

How the interface changes, and how you are told before anything you rely on
goes away. The normative rules are on the wiki page
[Spec: Compatibility and Versioning](https://github.com/mavrovde/viafrei-api/wiki/Spec-Compatibility-and-Versioning).
Where a rule below is not implemented yet, it says *planned*.

## Two version numbers

| Version | Where you see it | Changes when |
|---|---|---|
| Interface major | the path, `/v1` | only with a breaking change |
| Release version | `/v1/meta` `apiVersion`, image tags, the [changelog](changelog.html) | every release, following Semantic Versioning 2.0.0; 0.x releases are pre-releases |

The OpenAPI document's `info.version` describes the v1 interface itself; it is
`1.0.0-draft` until the first public release.

## What is a breaking change

Within `/v1` only non-breaking changes are made.

| Change | Breaking? |
|---|---|
| Adding an operation, an optional parameter, a response member, a warning code, a language, a source family | No |
| Adding a value to an open enumeration | No |
| Removing or renaming an operation, parameter or member | **Yes** |
| Changing a type, a format, a unit, the meaning of a value or a parameter's default | **Yes** |
| Making an optional parameter required, or narrowing accepted values | **Yes** |
| Changing the order of a list, or the status code of a case | **Yes** |
| Removing an enumeration value | **Yes** |
| Tightening a limit below its documented default | **Yes**; raising a limit is not |
| Changing an error `type` URI | **Yes** |
| Fixing a defect that contradicts the specification | No, but it is announced in the changelog |

So that additive changes do not break you:

- **Ignore members you do not know.** New members can appear in any object
  without notice.
- **Tolerate unknown enumeration values** in open enumerations, such as
  warning codes.
- **Branch on problem `type`**, never on `title` or `detail` ([error codes](errors.md)).

Every pull request runs a breaking-change diff of the OpenAPI document
against the default branch and against the last released document; a
breaking change inside `/v1` fails it.

## Deprecation and sunset

1. A breaking change ships as a new major path (`/v2`), served **beside** the
   old one.
2. The old major is then deprecated: its answers carry the `Deprecation` and
   `Sunset` headers (*planned*) for at least the notice period, designed as
   183 days (*planned* as a setting).
3. After the sunset date, every operation of the old major answers
   `404 not-found` with a `Link` to its successor for at least 90 further
   days (*planned*).
4. An operation is never removed without a sunset period.

Deprecations and sunsets are announced on the day they take effect in the
[changelog](changelog.html), on this portal and in `/v1/meta` under
`deprecations` (*planned*), and by e-mail to every active key holder at the
start of the notice period (*planned*).

## Stability of operations

*Planned:* a new operation may first be offered as beta, marked
`x-stability: beta` and reachable only with keys that opted in; it may
change without notice. Stable operations will be marked
`x-stability: stable`. No operation carries a marker yet; the
[endpoints](endpoints.html) page lists every operation with its marker once
it has one.
