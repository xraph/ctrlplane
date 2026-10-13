# Published e627 writer exclusion fixture

You can build this child independently from the current Ctrlplane checkout:

```sh
GOWORK=off GOTOOLCHAIN=go1.26.9 go mod verify
GOWORK=off GOTOOLCHAIN=go1.26.9 make f l test
GOWORK=off GOTOOLCHAIN=go1.26.9 go build -o /tmp/ctrlplane-olde627 .
/tmp/ctrlplane-olde627 identity
```

The module pins `github.com/xraph/ctrlplane` at
`v1.7.1-0.20261011163110-e627f3173e76`, published source commit
`e627f3173e76a7c23df8b99359ccb75eaa45fdc7`, with module sum
`h1:CUOpUx7/ESFCdNZTGETIikEEAJPyq+iU0PDkr0/HfXU=`. There are no
replacements. The `identity` command reads the compiled dependency metadata;
the outer test checks that metadata and logs the executable SHA-256.

From the repository root, point the test at a disposable PostgreSQL server and
this binary:

```sh
CTRLPLANE_TEST_POSTGRES_DSN='postgres://test_admin@localhost/test?sslmode=disable' \
CTRLPLANE_OLD_E627_BINARY=/tmp/ctrlplane-olde627 \
CTRLPLANE_WRITER_EXCLUSION_REQUIRED=1 \
GOWORK=off GOTOOLCHAIN=go1.26.9 \
go test -race ./store -run '^TestWriterExclusionActualOldE627Desired$' -count=1 -v
```

The test creates uniquely named databases and roles through the existing
exclusion fixture, then drops them. It needs an administrative setup principal.
The child itself connects as the restricted legacy role. You must use an isolated
server: the fixture checks that prepared transactions are disabled and installs
the floor only in its own database.

The unfenced control calls e627's actual `SetManagedDesired`, changes an active
target to paused and checks both revision increments and its retained receipt.
A separate database repeats that call after the grant cutover. Historical
`ReadManagedTarget` must still succeed, the mutation must return SQLSTATE `42501`,
and every retained public row and sequence must remain unchanged. If the
positive control fails or skips, denial cannot pass the parent test.

This characterizes e627's desired-state mutation. The successor-association loss
observed with the separate 8827 child is not attributed to e627. The oldb23a
module and binary remain unchanged. These tests do not establish complete
recovery, positive-protocol admission, production credential custody, restore
safety or container isolation.

The child accepts only `read_target` and `set_desired` over the bounded JSON-lines
protocol. It never migrates the database and has no provider or native-command
entry point. `source-identity.json` records the immutable source anchors and the
module identity used for this fixture.
