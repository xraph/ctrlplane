# Published B23a fixture

You can use this executable to exercise the published generation-one Ctrlplane
library against a disposable database written by the current library. Its
Ctrlplane pin stays at commit `8827a4f36fad2ce9d6d688aa8c4ea467da505ae3`.
Don't update it with the root module.

Build it explicitly with `GOWORK=off go build -trimpath -o /your/private/path/oldb23a .`.
The root tests accept its path through `CTRLPLANE_OLD_B23A_BINARY`. Set
`CTRLPLANE_OLD_B23A_REQUIRED=1` to make a missing binary or database fail the
campaign. Run `oldb23a identity` to check the embedded module version and sum.

Requests and responses are JSON lines with schema version `1` and a positive
sequential decimal `sequence`. You select an operation from the finite public
API whitelist in `main.go`; `payload` is its old typed request. The child reads
`CTRLPLANE_OLD_B23A_DSN` from its private environment. It never migrates the
database or sends native or provider commands.

Each process accepts at most 128 requests, 65,536 bytes per request, 8,192 bytes
per response and 1 MiB total output. API contexts last ten seconds and the
process context lasts sixty. Results contain a digest and explicit issue flags,
without command or evidence bytes. Errors use fixed classes.

The journal advance probe is separate from refusal tests. An old writer can
erase the successor association when advancing a journal with valid authority.
The isolated campaign reproduces that loss and checks that the current reader
rejects the damaged graph. Passing
refusal tests does not establish a writer floor or safe mixed-version use.
