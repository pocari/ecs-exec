# ecs-exec

An interactive wrapper around [ECS Exec](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs-exec.html) (`aws ecs execute-command`).

Select a cluster, task, and container with a fuzzy finder UI, type a command (defaults to `/bin/bash`), and get a shell in your running ECS container — no need to look up cluster names or task IDs.

```
select ecs cluster > my-cluster
select task > my-app-rails(ee65f2e75b694867a1e8455a27f05f17)
select container > rails
[default /bin/bash] >
```

## Requirements

- [AWS CLI](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)
- [Session Manager plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)
- ECS Exec enabled on the target service/task (`enableExecuteCommand`). See the [ECS Exec documentation](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs-exec.html) for IAM and task role requirements.

## Install

```sh
go install github.com/pocari/ecs-exec@latest
```

## Usage

```sh
ecs-exec
```

Credentials are resolved by the standard AWS SDK chain, so profiles (including SSO profiles) work as usual:

```sh
AWS_PROFILE=my-profile ecs-exec
```

### Options

| Flag | Description |
| ---- | ----------- |
| `-t` | Dry run: print the assembled `aws ecs execute-command` command instead of executing it |

## How it works

1. Lists ECS clusters and lets you pick one (fuzzy search)
2. Lists running tasks in the cluster, shown as `service-name(task-id)`
3. Lets you pick a container within the task
4. Prompts for a command (empty input runs `/bin/bash`)
5. Delegates to `aws ecs execute-command --interactive` for the actual session (the Session Manager plugin handles the interactive connection)

## License

[MIT](LICENSE)
