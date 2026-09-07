# Security

Report vulnerabilities privately through GitHub's security reporting feature when
enabled, or contact the repository owner privately. Do not put credentials or
live push targets into issues or test fixtures.

The API authenticates requests; Firestore client rules deny direct access. Host
applications own abuse controls, opt-in policy, content authorization, retention,
and account deletion. Firebase Auth revocation checks require the API service
account to read Firebase Auth user records. The self-service schedule API is opt-in
and must be mounted behind per-user limits. Keep credential files out of Git.

FCM sends and Firestore acknowledgements are not atomic. An ambiguous response can
cause a duplicate, and a notification already sent cannot be recalled after logout.
Treat notification text as visible on the user's lock screen. The library stores
registration addresses and account bindings in your infrastructure; it has no
library-owned collection endpoint. Firebase SDK collection and platform privacy
requirements still apply.

The Go audit is enforced in CI. The initial audit's remaining module-only notice
GO-2026-5932 concerns the unused `golang.org/x/crypto/openpgp` package (no maintained
fix). The library does not import OpenPGP. Recheck on each dependency update.
