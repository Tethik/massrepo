# Go template

Do security analysis work on all repositories in an org. With or without AI agents.

## Usage 🧑‍💻

- `make` or `make single-build` - build for just your arch. Outputs in `dist/`.
- `make build` - to build for all archs
- `make test` - to run tests

### Releases

To create a new release:

```sh
git tag -a vX.Y.Z # set your semantic version here
git push origin vX.Y.Z
```

Alternatively you can a manual release via make (not tested tbh)

`make release`
