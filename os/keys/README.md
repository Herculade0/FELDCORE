Signing keys for FELDCORE OS updates live outside the repository.

Generate a key pair with `go run ./cmd/osmanifest genkey -pub update.pub -key update.key`.
The public file is installed in the image at `/usr/share/feldcore/update.pub` and is safe to publish.
The private file is what `os/build.sh` reads from `FELDCORE_UPDATE_KEY`. The default build keeps an ephemeral key in `/var/tmp/feldcore-os/keys`, which is not this directory.

Do not commit a private key. Git ignores `*.key` and `*.sec` here.
