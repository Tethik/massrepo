# Development

## Building

```sh
make           # build for your arch, outputs to dist/
make build     # build for all architectures
make install   # go install ./cmd/massrepo into $GOBIN
make test      # run tests
```

Requires Go 1.22+.

## Releases

```sh
git tag -a vX.Y.Z
git push origin vX.Y.Z
```
