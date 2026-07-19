# RPM (Repo Manager)

Simple language-agnostic build and test tool with a development runtime for monorepos.

![TUI demo](https://github.com/vcnkl/rpm/raw/main/docs/media/tui-demo.gif "Env runtime TUI demo")

## Install

```shell
wget -qO- https://raw.githubusercontent.com/vcnkl/rpm/main/install.sh | sh
```

Recommended to check `.rpm/envs` into version control and ignore `.rpm/cache`.

## Commands

| Command                                    | Flags                                   | Description                                         |
|--------------------------------------------|-----------------------------------------|-----------------------------------------------------|
| `rpm init`                                 | -                                       | Run `init` checks and install steps, create `.rpm/` |
| `rpm build [<bundle>\|<bundle:target>]...` | `--force`, `--affected`, `--dry-run`    | Build `*_build` targets                             |
| `rpm test [<bundle>\|<bundle:target>]...`  | `--affected`, `--coverage`              | Run `*_test` targets                                |
| `rpm run [<bundle:target>]`                | -                                       | Run target                                          |
| `rpm env [<arg>]...`                       | See environments section below          | Run environment                                     |
| `rpm dev [<bundle:target>]`                | `--dry-run`                             | Run development mode                                |
| `rpm graph [<target>]`                     | `--format text\|json\|dot`, `--reverse` | Print dependency graph                              |

Global flags: `-d/--debug`, `-c/--config <path>`, `-j/--jobs <n>` and `--logs[=true|false]`

## repo.yml

Repo config options

| Key                        | Type   | Default   | Description                                                                                                                  |
|----------------------------|--------|-----------|------------------------------------------------------------------------------------------------------------------------------|
| `project.name`             | string | -         | Required. Used for generated resource names                                                                                  |
| `shell`                    | string | `/bin/sh` | Shell used for all commands                                                                                                  |
| `env.vars`                 | map    | -         | Environment variables applied to all commands                                                                                |
| `env.deps`                 | list   | -         | Docker dependencies                                                                                                          |
| `env.deps[].name`          | string | -         | Unique name referenced by bundles and blueprints                                                                             |
| `env.deps[].image`         | string | -         | `image:tag`                                                                                                                  |
| `env.deps[].env`           | map    | -         | Container environment                                                                                                        |
| `env.deps[].ports`         | list   | -         | Publishes an ephemeral host port and injects `<NAME>_PORT`. Use `VAR=port` to set a custom name in your target dotenv files. |
| `env.deps[].volumes`       | list   | -         | Container data paths - volume names are generated to prevent conflicts                                                       |
| `env.deps[].readiness-cmd` | string | -         | Must exit zero before targets start                                                                                          |
| `init`                     | list   | -         | Steps with `label`, `check_cmd` and `install_cmd` run by `rpm init`                                                          |
| `ignore`                   | list   | -         | Bundle path globs to ignore globally (good idea to ignore build directories here)                                            |
| `logger.datetime.format`   | string | `RFC3339` | Go time layout for log timestamps                                                                                            |
| `logs.enabled`             | bool   | `false`   | Write persistent JSON logs for build, test, dev and env up                                                                   |
| `logs.build.out`           | string | `log/rpm/build` | Build log directory relative to the repo root                                                                         |
| `logs.test.out`            | string | `log/rpm/test` | Test log directory relative to the repo root                                                                           |
| `logs.dev.out`             | string | `log/rpm/dev` | Dev log directory relative to the repo root                                                                             |
| `logs.env.out`             | string | `log/rpm/env` | Environment log directory relative to the repo root                                                                     |

```yaml
project:
  name: local-stack
shell: /usr/bin/env bash
logs:
  enabled: false
  build:
    out: log/rpm/build
  test:
    out: log/rpm/test
  dev:
    out: log/rpm/dev
  env:
    out: log/rpm/env
env:
  vars:
    APP_ENV: local
  deps:
    - name: postgres
      image: postgres:18
      env:
        POSTGRES_PASSWORD: postgres
      ports:
        - "POSTGRES_PASS=5432"
      volumes:
        - /var/lib/postgresql/data
      readiness-cmd: |
        timeout 90 sh -c 'until docker exec "$DOCKER_CONTAINER_NAME" pg_isready -q; do sleep 1; done'
    - name: redis
      image: redis:8.8.0
      ports:
        - "6379"
```

## rpm.yml

Bundle config options

| Key                               | Type           | Default | Description                                               |
|-----------------------------------|----------------|---------|-----------------------------------------------------------|
| `name`                            | string         | -       | Bundle name                                               |
| `env.variables`                   | map            | -       | Environment variables applied to all targets              |
| `env.deps`                        | list           | -       | Dependency names from `repo.yml`, started by `rpm env up` |
| `targets[].name`                  | string         | -       | Target name                                               |
| `targets[].cmd`                   | string or list | -       | Command to run                                            |
| `targets[].deps`                  | list           | -       | Targets that must return a zero exit code before running  |
| `targets[].in`                    | list           | -       | Input globs hashed for cache keys                         |
| `targets[].out`                   | list           | -       | Output paths that must exist for a cache hit              |
| `targets[].env`                   | map            | -       | Environment variables applied to target                   |
| `targets[].config.working_dir`    | string         | `local` | `local`, `repo_root` or a relative path                   |
| `targets[].config.reload`         | bool           | `true`  | Restart on file change under `rpm env up`                 |
| `targets[].config.ignore`         | list           | -       | Ignored globs for file watcher                            |
| `targets[].config.index`          | int            | `0`     | Before and main target startup order under `rpm env up`   |
| `targets[].config.readiness-cmd`  | string         | -       | Optional initial `rpm env up` gate after target launch    |
| `targets[].config.dotenv.enabled` | bool           | `true`  | Load `.env` from the working dir                          |
| `targets[].config.dotenv.files`   | list           | -       | Additional dotenv files                                   |

```yaml
name: products
env:
  variables:
    SERVICE: products
  deps:
    - postgres
    - redis
targets:
  - name: build
    cmd: go build -o .build/web .
    in:
      - "**/*.go"
    out:
      - .build/web
  - name: web_serve
    cmd: ./web_serve.sh
    config:
      working_dir: local
      reload: true
      index: -1
      readiness-cmd: |
        timeout 30 sh -c 'until curl -fsS http://localhost:8080/ready; do sleep 1; done'
```

## Environments

| Command                   | Flags                                                                | Description                 |
|---------------------------|----------------------------------------------------------------------|-----------------------------|
| `rpm env create <name>`   | `--target ref`, `--before ref`                                       | Create a blueprint          |
| `rpm env edit <name>`     | `--add-target`, `--remove-target`, `--add-before`, `--remove-before` | Change refs                 |
| `rpm env validate <name>` | -                                                                    | Validate all refs resolve   |
| `rpm env render <name>`   | `--out path`                                                         | Generate `runtime.gen.star` |
| `rpm env up <name>`       | `--no-reload`, `--no-deps`, `--non-interactive`                      | Start                       |
| `rpm env down <name>`     | -                                                                    | Stop                        |
| `rpm env prune <name>`    | -                                                                    | Remove cached resources     |

```yaml
version: 1
name: local-stack
live_reload:
  enabled: true
  debounce: 200ms
before:
  - products:migrate-pg
targets:
  - ref: products:web_serve
    reload: true
  - ref: products:grpc_serve
    reload: false
dependencies:
  enabled: true
  include:
    - postgres
    - redis
  exclude: []
variables:
  LOG_LEVEL: debug
```

## Environment variables

Merged in the following order: host env, repo config `env.vars`, `REPO_ROOT`, `BUNDLE_ROOT`, bundle `env.variables`, target `env`, blueprint `variables`, blueprint target `env`, target dotenv files.
