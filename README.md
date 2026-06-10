# RPM (Repo Manager)

Language-agnostic build orchestration and local environment runtime for monorepos.

## Command Model

RPM has a clear split between build/test/run orchestration and environment runtime orchestration:

- `rpm build` builds filesystem-output targets only.
- `rpm env create`, `rpm env render` and `rpm env up` handle local environment workflows.
- Target suffixes are ordinary target name text; environment membership comes from explicit blueprint target refs.
- Environment containers are runtime dependencies declared once in `repo.yml` and referenced by name from bundles.

## Installation

```shell
wget -qO- https://raw.githubusercontent.com/vcnkl/rpm/main/install.sh | sh
```

## Configuration

### repo.yml (Repository Root)

```yaml
project:
  name: 'my-project'          # Required; used for generated runtime resource names
shell: '/usr/bin/env bash'    # Default shell for commands
env:
  vars:                       # Global environment variables
    PROJECT: 'my-project'
  deps:                       # Docker runtime dependencies available to bundles
    - name: postgres
      image: postgres:16
      env:
        POSTGRES_PASSWORD: example
      ports:
        - "5432"                  # Publishes an ephemeral host port; injects POSTGRES_PORT
      volumes:                # Container data paths only; Docker volume names are generated
        - /var/lib/postgresql/data
      readiness-cmd: |
        timeout 90s bash -c "until docker exec ${DOCKER_CONTAINER_NAME} pg_isready; do sleep 5; done"
    - name: redis
      image: redis:7
logger:
  datetime:
    format: '2006-01-02T15:04:05Z07:00' # Go time layout for rpm log timestamps
init:                         # External dependencies to check/install
  - label: node
    check_cmd: 'node --version'
    install_cmd: 'nvm install 20'
ignore:
  - 'path/to/ignored/bundle/*'
```

### rpm.yml (Bundle Configuration)

```yaml
name: my-service              # Bundle name (used in target IDs)
env:                          # Environment-related bundle configuration
  variables:                  # Bundle-level environment variables
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
      index: 1                # Optional before-target ordering hint for rpm env up
      dotenv:
        enabled: true         # Load .env from bundle directory
      reload: true            # Environment runtime reloads on file changes
      ignore:                 # Environment runtime ignore patterns
        - 'tmp'
        - '*.log'
```

Bundle environment dependency requirements are declared under `env.deps` as names that must exist in top-level `repo.yml` `env.deps`. They are only used by `rpm env up`; they are not build outputs and do not participate in build cache validation.

```yaml
name: api
env:
  deps:
    - postgres
    - redis
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
rpm env create [blueprint] --target bundle:target
rpm env create [blueprint] --target bundle:target --before bundle:migrate
rpm env edit <blueprint> --add-target bundle:target --add-before bundle:migrate
rpm env validate <blueprint>
rpm env render <blueprint> [--out path]
rpm env up <blueprint> [--non-interactive] [--no-reload] [--no-deps]
rpm env down <blueprint>
rpm env prune <blueprint>
```

Blueprint files:
- Edit `.rpm/envs/<name>/config.yml`.
- Commit `.rpm/envs/<name>/runtime.gen.star`.
- Generated Starlark stores refs, order and repo-relative config paths.
- Commands and dotenv values are resolved at runtime.
- Targets are explicit refs. RPM does not infer dev targets from suffixes.

```yaml
version: 1
name: local-stack
live_reload:
  enabled: true
  debounce: 100ms
before:
  - go-app:migrate
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
    - postgres
    - redis
  exclude: []
variables:
  LOG_LEVEL: debug
```

Create a blueprint:
```bash
rpm env create --non-interactive local --target api:serve --before api:migrate
rpm env create
```

Runtime rules:
- `before` refs run after dependencies and before targets.
- `config.index` orders `before` refs first, then unordered refs sort by ref.
- Selected bundles must declare required `env.deps`.
- Bare ports like `"5432"` get ephemeral host ports.
- Mapped ports like `"5432:5432"` stay fixed.
- Injected port env wins over `.env` values.
- Port env names: `POSTGRES_PORT`, `MAILHOG_PORT_1025` or custom `"MONGO_PORT=27017"`.
- `readiness-cmd` runs after container start with `DOCKER_CONTAINER_NAME`.
- `--no-deps` skips containers, port env injection and readiness commands.
- Volume names are cached in `.rpm/cache/env-volumes.json`; reset with `rpm env prune <blueprint>`.
- Live reload defaults: `enabled: true`, `debounce: 100ms`.

Render and run:
```bash
rpm env render local
rpm env up local
rpm env up local --non-interactive
```

Notes:
- `create`, `edit` and `render` update `runtime.gen.star`.
- `up` reads `runtime.gen.star`, resolves current repo config, starts deps, runs `before` refs then starts targets.
- Watched file changes restart affected targets.
- Dotenv uses target config: `.env` by default plus `config.dotenv.files`.
- If `runtime.gen.star` is missing, run `rpm env render <blueprint>`.
- Interactive mode opens the Ink TUI; `--non-interactive` streams JSON events.

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
- Stack `before` target refs use the same target command, working directory, environment and dotenv behavior as normal targets.
- Watch roots default to the bundle root or the target `in` patterns, and ignore entries come from target config.
- `--no-reload` disables watchers at runtime without mutating the committed blueprint.
- `--no-deps` skips dependency containers while still running targets.
- Runtime dependencies use Docker CLI orchestration: network creation, volume creation, detached containers, container removal and network removal.
- Runtime planning is intentionally modular: blueprint loading, normalized environment spec, Starlark generation, Starlark evaluation, runtime interfaces and the centralized `environments/tui` bridge are separate packages so future commands can compose them without changing blueprint semantics.
