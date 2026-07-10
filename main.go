package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/ktr0731/go-fuzzyfinder"
)

const defaultCommand = "/bin/bash"

// describe-tasksの1回あたりのタスク数上限(API仕様)
const describeTasksBatchSize = 100

type task struct {
	id    string
	group string
}

// ECS APIのうち利用する操作(テストでモック差し替えするためのインターフェース)
type ecsAPI interface {
	ListClusters(ctx context.Context, in *ecs.ListClustersInput, opts ...func(*ecs.Options)) (*ecs.ListClustersOutput, error)
	ListTasks(ctx context.Context, in *ecs.ListTasksInput, opts ...func(*ecs.Options)) (*ecs.ListTasksOutput, error)
	DescribeTasks(ctx context.Context, in *ecs.DescribeTasksInput, opts ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error)
}

func main() {
	dumpOnly := flag.Bool("t", false, "実行せずに aws ecs execute-command のコマンド文字列を表示する")
	flag.Parse()

	if err := run(context.Background(), *dumpOnly); err != nil {
		// fzfのキャンセル(Esc/Ctrl-C)は正常な中断としてメッセージなしで終了する
		if err == fuzzyfinder.ErrAbort {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dumpOnly bool) error {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return err
	}
	client := ecs.NewFromConfig(cfg)

	cluster, err := selectCluster(ctx, client)
	if err != nil {
		return err
	}

	taskID, err := selectTask(ctx, client, cluster)
	if err != nil {
		return err
	}

	container, err := selectContainer(ctx, client, cluster, taskID)
	if err != nil {
		return err
	}

	execCommand, err := readExecCommand(os.Stdin)
	if err != nil {
		return err
	}

	if dumpOnly {
		fmt.Printf("aws ecs execute-command --cluster %s --task %s --container %s --interactive --command '%s'\n",
			cluster, taskID, container, execCommand)
		return nil
	}

	// execute-commandの対話セッションはsession-manager-pluginが必要なため、
	// SDKで完結させずaws CLIに委譲する
	cmd := exec.CommandContext(ctx, "aws", "ecs", "execute-command",
		"--cluster", cluster,
		"--task", taskID,
		"--container", container,
		"--interactive",
		"--command", execCommand,
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func selectCluster(ctx context.Context, client ecsAPI) (string, error) {
	var clusters []string
	p := ecs.NewListClustersPaginator(client, &ecs.ListClustersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return "", err
		}
		for _, arn := range page.ClusterArns {
			clusters = append(clusters, arnResource(arn))
		}
	}
	if len(clusters) == 0 {
		return "", fmt.Errorf("ECSクラスタが見つかりません")
	}

	idx, err := fuzzyfinder.Find(clusters,
		func(i int) string { return clusters[i] },
		fuzzyfinder.WithPromptString("select ecs cluster > "),
	)
	if err != nil {
		return "", err
	}
	return clusters[idx], nil
}

func selectTask(ctx context.Context, client ecsAPI, cluster string) (string, error) {
	tasks, err := listActiveTasks(ctx, client, cluster)
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "", fmt.Errorf("クラスタ %s に稼働中のタスクがありません", cluster)
	}

	idx, err := fuzzyfinder.Find(tasks,
		func(i int) string { return tasks[i].display() },
		fuzzyfinder.WithPromptString("select task > "),
	)
	if err != nil {
		return "", err
	}
	return tasks[idx].id, nil
}

func (t task) display() string {
	return fmt.Sprintf("%s(%s)", t.group, t.id)
}

func listActiveTasks(ctx context.Context, client ecsAPI, cluster string) ([]task, error) {
	var arns []string
	p := ecs.NewListTasksPaginator(client, &ecs.ListTasksInput{Cluster: aws.String(cluster)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		arns = append(arns, page.TaskArns...)
	}

	var tasks []task
	for start := 0; start < len(arns); start += describeTasksBatchSize {
		end := min(start+describeTasksBatchSize, len(arns))
		out, err := client.DescribeTasks(ctx, &ecs.DescribeTasksInput{
			Cluster: aws.String(cluster),
			Tasks:   arns[start:end],
		})
		if err != nil {
			return nil, err
		}
		for _, t := range out.Tasks {
			// groupは "service:xxx" 形式で返るのでプレフィックスを除いて表示する
			group := strings.TrimPrefix(aws.ToString(t.Group), "service:")
			tasks = append(tasks, task{
				id:    arnResource(aws.ToString(t.TaskArn)),
				group: group,
			})
		}
	}
	return tasks, nil
}

func selectContainer(ctx context.Context, client ecsAPI, cluster, taskID string) (string, error) {
	out, err := client.DescribeTasks(ctx, &ecs.DescribeTasksInput{
		Cluster: aws.String(cluster),
		Tasks:   []string{taskID},
	})
	if err != nil {
		return "", err
	}
	if len(out.Tasks) == 0 {
		return "", fmt.Errorf("タスク %s が見つかりません", taskID)
	}

	var containers []string
	for _, c := range out.Tasks[0].Containers {
		containers = append(containers, aws.ToString(c.Name))
	}

	idx, err := fuzzyfinder.Find(containers,
		func(i int) string { return containers[i] },
		fuzzyfinder.WithPromptString("select container > "),
	)
	if err != nil {
		return "", err
	}
	return containers[idx], nil
}

func readExecCommand(r io.Reader) (string, error) {
	fmt.Printf("[default %s] > ", defaultCommand)
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultCommand, nil
	}
	return line, nil
}

// ARNの最後のリソース名部分("arn:aws:ecs:...:cluster/name" の name)を取り出す
func arnResource(arn string) string {
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}
