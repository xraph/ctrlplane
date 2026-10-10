# Dependency checks

Use Node 22.23.3 and pnpm 10.34.6 for the docs app. CI installs the frozen lockfile, audits the complete graph, checks types and lint warnings, and builds the production site.

The October 10, 2026 docs checkpoint selects Next 16.3.8, Fumadocs core/UI 16.8.5, MDX 14.3.2 and PostCSS 8.5.29. React 19.2.4 and Tailwind 4.1.18 remain unchanged. The resolved graph contains 372 dependencies and the full pnpm audit reports zero advisories. Removed packages include image-size, path-to-regexp and postcss-selector-parser.

Keep the `mdast-util-to-markdown` 2.1.2 override while this Fumadocs family wraps Markdown handlers. Version 2.2.0 recurses with those handlers in the reviewed sibling app. Remove the override when upstream changes the handlers and you can verify both processed Markdown routes and the full audit without it. This pin handles compatibility; it does not exclude security findings.

Production checks cover search, sidebar and relative Markdown links at desktop and narrow widths, processed Markdown, Open Graph PNG output, and missing-page responses. The Tailwind logical inset aliases are checked with positive, negative and fractional values in both text directions. The existing favicon 404 and unset metadataBase warning remain.

Go dependencies and provider compatibility are being qualified in a separate checkpoint. This docs publication does not qualify deployment, rollback, a live Kubernetes cluster or the installed Docker daemon. The local containerd 2.3.5 daemon is affected by [GHSA-pg57-6jwg-q645](https://github.com/containerd/containerd/security/advisories/GHSA-pg57-6jwg-q645), which permits resource exhaustion during malicious OCI index pulls. Trusted fixture checks cannot establish resilience against that attack.

The task evidence is retained in the Dispatch dependency qualification workspace as `task-3-docs-report.md` and `task-3-evidence/`. GitHub alert dispositions are recorded there after publication; audit results alone are not an alert-closure inventory.
