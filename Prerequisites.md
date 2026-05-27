# Prerequisites

Install all prerequisites for the project

## Add `.pre-commit-config.yaml` in the project root

```yaml
# ..pre-commit-config.yaml
repos:
  - repo: https://github.com/Yelp/detect-secrets
    rev: v1.5.0
    hooks:
      - id: detect-secrets
        args: ['--baseline', '.secrets.baseline']
```

## Install `pre-commit` hook

- Install `pre-commit` module

```shell
pip install pre-commit
or
brew install pre-commit
```

- Install Git hooks

This command installs git hooks into `.git/hooks/pre-commit`

```shell
pre-commit install
```

- Update `pre-commit`

Ensure `pre-commit` version is v1.5.0 (match to `.pre-commit-config.yaml.repos.rev`).
If you get new version, please update rev value in `pre-commit-config.yaml`

```shell
pre-commit autoupdate
```

- Install `detect-secrets`

Ensure detect-secrets version is v1.5.0

```shell
pip install detect-secrets
or
brew install detect-secrets
```

- Generate `.secrets.baseline`

```shell
detect-secrets scan > .secrets.baseline
```