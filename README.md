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

Environment blueprints are committed directories stored under `.rpm/envs/<name>/`. The editable YAML lives at `.rpm/envs/<name>/config.yml`, and generated Starlark lives at `.rpm/envs/<name>/runtime.gen.star` so the selected refs and execution order can be reviewed and committed. Generated Starlark uses repo-relative config paths such as `repo.yml` and `apps/api/rpm.yml`; it does not embed target command text or loaded dotenv values. Blueprints select explicit target refs, and RPM derives dependency refs from selected bundles; RPM does not infer dev targets from suffixes.

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

Use `rpm env create --non-interactive <name> --target bundle:target --before bundle:migrate` to create a blueprint from flags, or run `rpm env create` for a prompt-based flow. `before` entries run after dependencies and before target processes; they reference existing `rpm.yml` targets. Selected before targets with `config.index` run first by ascending index, ties are sorted by target ref, and selected before targets without `config.index` run afterward by target ref. Bundle `env.deps` are mandatory for selected target and before-target bundles; generated blueprints record the sorted required dependency refs and clear dependency excludes. Bare dependency ports such as `"5432"` are published on dynamically selected ephemeral host ports; explicit mappings such as `"5432:5432"` are preserved. For every started dependency port, `rpm env up` injects the resolved host port into before and target process environments after configured `.env` values so the dynamic value wins on name conflicts. A dependency with one port uses `<DEPENDENCY_NAME>_PORT`, such as `POSTGRES_PORT`; a dependency with multiple ports uses `<DEPENDENCY_NAME>_PORT_<CONTAINER_PORT>`, such as `MAILHOG_PORT_1025`. Custom env names can be declared by prefixing a port with `NAME=`, such as `"MONGO_PORT=27017"`. Dependencies can define `readiness-cmd`; when present, `rpm env up` runs it after the container starts and before any `before` targets, with `DOCKER_CONTAINER_NAME` and the resolved dependency port env vars available. `--no-deps` skips dependency containers and therefore does not inject dependency port env vars or run readiness commands. Docker volume names are generated and cached in `.rpm/cache/env-volumes.json`; `rpm env prune <blueprint>` resets cached volume names for one blueprint. `live_reload.enabled` defaults to `true`, `live_reload.debounce` defaults to `100ms`, and target reload defaults come from each bundle target's `config.reload`; `targets[].reload` is an explicit override that is still gated by the blueprint-level live reload switch.

`rpm env render <blueprint>` validates the blueprint and writes deterministic Starlark under `.rpm/envs/<blueprint>/runtime.gen.star`. `rpm env create` and `rpm env edit` also update this generated Starlark after writing `.rpm/envs/<blueprint>/config.yml`. `rpm env up <blueprint>` reads the generated Starlark file, resolves current target commands, working directories, dotenv files, watch roots, and dependency definitions from the referenced `repo.yml` and `rpm.yml` files, starts dependency containers, runs `before` targets, starts target processes, and restarts affected target processes when watched files change. Dotenv resolution follows each target's current config: `.env` is loaded by default when dotenv is enabled, and `config.dotenv.files` adds target-specific file paths/globs. If `runtime.gen.star` is missing, run `rpm env render <blueprint>` before `rpm env up`. In interactive mode it opens the centralized Ink TUI from `ui/env-tui`; in `--non-interactive` mode it streams newline-delimited JSON runtime events.

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
- Runtime planning is intentionally modular: blueprint loading, normalized environment spec, Starlark generation, Starlark evaluation, runtime interfaces and the centralized `ui/env-tui` bridge are separate packages so future commands can compose them without changing blueprint semantics.
