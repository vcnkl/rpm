# RPM (Repo Manager)

Language-agnostic build orchestration and local environment runtime for monorepos.

## Command Model

RPM has a clear split between build/test/run orchestration and environment runtime orchestration:

- `rpm build` builds filesystem-output targets only.
- `rpm env create`, `rpm env render` and `rpm env up` handle local environment workflows.
- Target suffixes are ordinary target name text; environment membership comes from explicit blueprint target refs.
- Environment containers are runtime dependencies declared under bundle `dependencies`.

## Installation

```shell
wget -qO- https://raw.githubusercontent.com/vcnkl/rpm/main/install.sh | sh
```

## Configuration

### repo.yml (Repository Root)

```yaml
shell: '/usr/bin/env bash'    # Default shell for commands
env:                          # Global environment variables
  PROJECT: 'my-project'
logger:
  datetime:
    format: '2006-01-02T15:04:05Z07:00' # Go time layout for rpm log timestamps
deps:                         # External dependencies to check/install
  - label: node
    check_cmd: 'node --version'
    install_cmd: 'nvm install 20'
ignore:
  - 'path/to/ignored/bundle/*'
```

### rpm.yml (Bundle Configuration)

```yaml
name: my-service              # Bundle name (used in target IDs)
env:                          # Bundle-level environment variables
  SERVICE_PORT: '8080'
targets:
  - name: build               # Target name → ID becomes "my-service:build"
    deps:                     # Dependencies (other targets)
      - common:codegen
    in:                       # Input files/globs for cache key
      - '**/*.go'
      - 'go.mod'
    out:                      # Output files to check for cache validity
      - '.build/my-service'
    env:                      # Target-level environment variables
      CGO_ENABLED: '1'
    cmd: 'go build -o .build/my-service .'
    config:
      working_dir: 'local'    # 'local' (bundle dir), 'repo_root', or relative path
      dotenv:
        enabled: true         # Load .env from bundle directory
      reload: true            # Environment runtime reloads on file changes
      ignore:                 # Environment runtime ignore patterns
        - 'tmp'
        - '*.log'
```

Bundle-level environment dependencies are declared next to targets and use tagged Docker image references. They are only used by `rpm env up`; they are not build outputs and do not participate in build cache validation.

```yaml
name: api
dependencies:
  - name: postgres
    image: postgres:16
    mode: shared              # one container per blueprint dependency
    env:
      POSTGRES_PASSWORD: example
    ports:
      - "5432:5432"
    volumes:
      - postgres-data:/var/lib/postgresql/data
  - name: redis
    image: redis:7
    mode: dedicated           # one container per selected target in this bundle
targets:
  - name: echo-123
    cmd: go run .
```

## Commands

**Important**: Flags must come BEFORE target names (urfave/cli requirement).

### build
```bash
rpm build [targets...]              # Build specific targets
rpm build                           # Build all *_build targets
rpm build --force core              # Force rebuild (ignore cache)
rpm build --dry-run core            # Show what would be built
rpm build -j 4 core                 # Limit parallel jobs
```

### test
```bash
rpm test [targets...]               # Run specific test targets
rpm test                            # Run all *_test targets
```

### env
```bash
rpm env create [blueprint] --target bundle:target [--deps]
rpm env edit <blueprint> --add-target bundle:target
rpm env validate <blueprint>
rpm env render <blueprint> [--out path]
rpm env up <blueprint> [--non-interactive] [--no-reload] [--no-deps] [--render-only]
rpm env down <blueprint>
```

Environment blueprints are committed YAML files stored in `.rpm/envs/<name>.yml`. They select explicit target refs and dependency refs; RPM does not infer dev targets from suffixes.

```yaml
version: 1
name: local-stack
live_reload:
  enabled: true
  debounce: 100ms
pre:
  - go-app:migrate
  - go-app:scripts/bootstrap.sh
  - /scripts/repo-bootstrap.sh
  - |
    echo "inline pre"
targets:
  - ref: go-app:echo-123
    reload: true
    env:
      APP_PORT: "8080"
  - ref: ts-app:web
    reload: true
dependencies:
  enabled: true
  include:
    - go-app:postgres
    - python-app:redis
  exclude: []
variables:
  LOG_LEVEL: debug
```

Use `rpm env create --non-interactive <name> --target bundle:target --deps` to create a blueprint from flags, or run `rpm env create` for a prompt-based flow. `pre` entries run after dependencies and before target processes; they can reference other `rpm.yml` targets, bundle/repo script paths, or inline YAML pipe commands. `dependencies.include` limits which bundle dependencies start, `dependencies.exclude` removes refs from the selected dependency set, and `dependencies.enabled: false` skips containers completely. `live_reload.enabled` defaults to `true`, `live_reload.debounce` defaults to `100ms`, and `targets[].reload` overrides the blueprint-level live reload setting per target.

`rpm env render <blueprint>` validates the blueprint, resolves repo/bundle/target config, and writes deterministic Starlark under `.rpm/cache/starlark/<blueprint>/env.star`. `rpm env up <blueprint>` runs the same validation and render pipeline, evaluates the generated Starlark runtime plan, starts dependency containers, runs `pre` scripts, starts target processes, and restarts affected target processes when watched files change. In interactive mode it opens the embedded React/Ink TUI; in `--non-interactive` mode it streams newline-delimited JSON runtime events.

`rpm env down <blueprint>` removes dependency containers and the environment network for that blueprint. It does not stop arbitrary external processes.

### run
```bash
rpm run <target>                    # Run any target by exact ID
rpm run core:migrate                # Example: run migration target
```

### init
```bash
rpm init                            # Initialize .rpm directory and validate config
```

### graph
```bash
rpm graph [target]                  # Show dependency graph
```

## Global Flags

- `--debug, -d`: Enable debug logging
- `--config, -c`: Path to repo.yml (default: auto-detect via git root)
- `--jobs, -j`: Max parallel jobs (default: NumCPU)

## Environment Variables

Composed in order (later overrides earlier):
1. System environment
2. repo.yml `env`
3. `REPO_ROOT` (auto-set)
4. `BUNDLE_ROOT` (auto-set)
5. Bundle `env`
6. Target `env`
7. Blueprint `variables`
8. Blueprint target `env`
9. `.env` file (if `config.dotenv.enabled`)

## Caching

- Input hash: SHA256 of all files matching `in` patterns
- Generated state is stored under ignored `.rpm/cache/`, including build cache, DAG cache and generated Starlark.
- Cache hit requires: same input hash + all `out` files exist
- Dependency rebuild propagates to dependents

## Environment Runtime

- Target commands use the resolved working directory from `config.working_dir`.
- Target environments compose values in this order: host env, repo env, `REPO_ROOT`, `BUNDLE_ROOT`, bundle env, target env, blueprint variables, blueprint target env and configured dotenv files.
- Stack `pre` target refs use the same target command, working directory, environment and dotenv behavior. Script path entries use the same path conventions: `/path` from `REPO_ROOT`, `bundle:path` from a bundle root, and current-bundle paths where a bundle context exists.
- Watch roots default to the bundle root or the target `in` patterns, and ignore entries come from target config.
- `--no-reload` disables watchers at runtime without mutating the committed blueprint.
- `--no-deps` skips dependency containers while still running targets.
- Runtime dependencies use Docker CLI orchestration: network creation, volume creation, detached containers, container removal and network removal.
- Runtime planning is intentionally modular: blueprint loading, normalized environment spec, Starlark generation, Starlark evaluation, runtime interfaces and TUI bridge are separate packages so future commands can compose them without changing blueprint semantics.
