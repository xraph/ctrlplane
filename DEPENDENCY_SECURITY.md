# Dependency checks

Use Node 22.23.3 and pnpm 10.34.6 for the docs app. CI installs the frozen lockfile, audits the complete graph, checks types and lint warnings, and builds the production site.

The October 10, 2026 docs checkpoint selects Next 16.3.8, Fumadocs core/UI 16.8.5, MDX 14.3.2 and PostCSS 8.5.29. React 19.2.4 and Tailwind 4.1.18 remain unchanged. The resolved graph contains 372 dependencies and the full pnpm audit reports zero advisories. Removed packages include image-size, path-to-regexp and postcss-selector-parser.

Keep the `mdast-util-to-markdown` 2.1.2 override while this Fumadocs family wraps Markdown handlers. Version 2.2.0 recurses with those handlers in the reviewed sibling app. Remove the override when upstream changes the handlers and you can verify both processed Markdown routes and the full audit without it. This pin handles compatibility; it does not exclude security findings.

Production checks cover search, sidebar and relative Markdown links at desktop and narrow widths, processed Markdown, Open Graph PNG output, and missing-page responses. The Tailwind logical inset aliases are checked with positive, negative and fractional values in both text directions. The existing favicon 404 and unset metadataBase warning remain.

Use Go 1.26.9 with `GOWORK=off` and no module replacements. The supported Docker SDK uses `github.com/moby/moby/client` 0.6.2 and `github.com/moby/moby/api` 1.56.1. Helm remains on its maintained 3.22.0 family with Kubernetes modules 0.37.1 and ORAS 2.6.2. The legacy Docker and containerd daemon modules are absent from the resolved application graph.

CI blocks called vulnerable symbols and retains the complete import/test, program and actual binary scanner output. It also records module replacements, package graphs and binary build metadata. `GO-2026-5932` remains a module-level finding in `golang.org/x/crypto` 0.57.0, with no published fix. The affected OpenPGP packages are absent from the imported and compiled graphs; Helm uses ProtonMail OpenPGP instead. Keep the finding visible when you change dependencies or imports.

The Docker client negotiates API versions lazily on ordinary requests. Its explicit Ping negotiation rejects versions below 1.40, but ordinary request construction ignores that negotiation error and can send API 1.56. This is not a client-side minimum-version gate. Environment API overrides and explicit host precedence are preserved, and health Ping checks reachability without negotiation.

Stats requests keep the existing one-shot decode and error behavior. Each request now has a child context that is canceled after its successful response body is closed. The SDK error reader stops at 1 MiB and can return without giving its body to the caller. With Go's HTTP transport, cancellation terminates a capped response that remains open; short and empty error bodies reach EOF and allow connection reuse. The SDK still does not call `Body.Close` on those error bodies. This mitigation does not establish cleanup for arbitrary custom transports. Logs and successful streaming keep their existing ownership and cancellation behavior.

The Helm checks cover local archives, explicit repository versions, caching, missing files and upstream provenance verification. The provider's default loader does not enable provenance verification or expose a keyring policy. Helm 3.22 supports Kubernetes 1.34 through 1.37 and reaches security maintenance end on February 10, 2027. A live cluster and OCI registry remain unverified.

These dependency checks do not qualify deployment, drain, rollback or the installed Docker daemon. The local containerd 2.3.5 daemon is affected by [GHSA-pg57-6jwg-q645](https://github.com/containerd/containerd/security/advisories/GHSA-pg57-6jwg-q645), which permits resource exhaustion during malicious OCI index pulls. Trusted fixture checks cannot establish resilience against that attack.

The task evidence is retained in the Dispatch dependency qualification workspace as `task-3-docs-report.md`, `task-3-report.md` and `task-3-evidence/`. GitHub alert dispositions are recorded there after publication; audit results alone are not an alert-closure inventory.
