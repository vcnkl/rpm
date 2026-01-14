# RPM (Repo Manager)

Build orchestration tool for monorepos.

## Configuration

### repo.yml (Repository Root)

```yaml
shell: '/usr/bin/env bash'    # Default shell for commands
env:                          # Global environment variables
  PROJECT: 'my-project'
docker:                       # Docker backend configuration
  backend: local              # or 'remote'
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
      reload: true            # For dev mode: restart on file changes
      ignore:                 # For dev mode: ignore patterns
        - 'tmp'
        - '*.log'
```

## Commands

**Important**: Flags must come BEFORE target names (urfave/cli requirement).

### build
```bash
rpm build [targets...]              # Build specific targets
rpm build                           # Build all *_build targets
rpm build --docker                  # Build all *_image targets
rpm build --force core              # Force rebuild (ignore cache)
rpm build --dry-run core            # Show what would be built
rpm build -j 4 core                 # Limit parallel jobs
```

### test
```bash
rpm test [targets...]               # Run specific test targets
rpm test                            # Run all *_test targets
```

### dev
```bash
rpm dev [targets...]                # Start dev mode for *_dev targets
rpm dev --dry-run core              # Show what would run without executing
rpm dev --no-deps core              # Don't start dependency dev targets
```

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
7. `.env` file (if `config.dotenv.enabled`)

## Caching

- Input hash: SHA256 of all files matching `in` patterns
- Cache stored in `.rpm/builds.json`
- Cache hit requires: same input hash + all `out` files exist
- Dependency rebuild propagates to dependents

## Dev Mode

- Watches bundle directory for file changes
- Respects `config.ignore` patterns
- `config.reload: true` (default): Restarts process on change
- `config.reload: false`: Runs once without watching
- Process groups for clean shutdown (SIGTERM → SIGKILL)
