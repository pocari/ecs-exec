package main

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ecsAPIのモック。各フィールドに挙動を差し込む
type mockECS struct {
	listClusters  func(*ecs.ListClustersInput) (*ecs.ListClustersOutput, error)
	listTasks     func(*ecs.ListTasksInput) (*ecs.ListTasksOutput, error)
	describeTasks func(*ecs.DescribeTasksInput) (*ecs.DescribeTasksOutput, error)
}

func (m *mockECS) ListClusters(_ context.Context, in *ecs.ListClustersInput, _ ...func(*ecs.Options)) (*ecs.ListClustersOutput, error) {
	return m.listClusters(in)
}

func (m *mockECS) ListTasks(_ context.Context, in *ecs.ListTasksInput, _ ...func(*ecs.Options)) (*ecs.ListTasksOutput, error) {
	return m.listTasks(in)
}

func (m *mockECS) DescribeTasks(_ context.Context, in *ecs.DescribeTasksInput, _ ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error) {
	return m.describeTasks(in)
}

func TestArnResource(t *testing.T) {
	tests := []struct {
		name string
		arn  string
		want string
	}{
		{"クラスタARN", "arn:aws:ecs:ap-northeast-1:123456789012:cluster/my-cluster", "my-cluster"},
		{"タスクARN", "arn:aws:ecs:ap-northeast-1:123456789012:task/my-cluster/ee65f2e75b694867a1e8455a27f05f17", "ee65f2e75b694867a1e8455a27f05f17"},
		{"スラッシュなしはそのまま", "my-cluster", "my-cluster"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := arnResource(tt.arn); got != tt.want {
				t.Errorf("arnResource(%q) = %q, want %q", tt.arn, got, tt.want)
			}
		})
	}
}

func TestTaskDisplay(t *testing.T) {
	got := task{id: "ee65f2e75b694867a1e8455a27f05f17", group: "my-app-rails"}.display()
	want := "my-app-rails(ee65f2e75b694867a1e8455a27f05f17)"
	if got != want {
		t.Errorf("display() = %q, want %q", got, want)
	}
}

func TestReadExecCommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"空エンターはデフォルト", "\n", "/bin/bash"},
		{"空白のみもデフォルト", "   \n", "/bin/bash"},
		{"入力があればそれを使う", "rails console\n", "rails console"},
		{"前後の空白は除去", "  ls -la  \n", "ls -la"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readExecCommand(strings.NewReader(tt.input))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("readExecCommand(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestListActiveTasks(t *testing.T) {
	client := &mockECS{
		listTasks: func(in *ecs.ListTasksInput) (*ecs.ListTasksOutput, error) {
			// 2ページに分けて返し、ページネーションを確認する
			if in.NextToken == nil {
				return &ecs.ListTasksOutput{
					TaskArns:  []string{"arn:aws:ecs:ap-northeast-1:1:task/c/task1"},
					NextToken: aws.String("page2"),
				}, nil
			}
			return &ecs.ListTasksOutput{
				TaskArns: []string{"arn:aws:ecs:ap-northeast-1:1:task/c/task2"},
			}, nil
		},
		describeTasks: func(in *ecs.DescribeTasksInput) (*ecs.DescribeTasksOutput, error) {
			var out ecs.DescribeTasksOutput
			for _, arn := range in.Tasks {
				out.Tasks = append(out.Tasks, types.Task{
					TaskArn: aws.String(arn),
					Group:   aws.String("service:my-app-rails"),
				})
			}
			return &out, nil
		},
	}

	tasks, err := listActiveTasks(context.Background(), client, "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	if tasks[0].id != "task1" || tasks[1].id != "task2" {
		t.Errorf("task ids = %q, %q, want task1, task2", tasks[0].id, tasks[1].id)
	}
	// service: プレフィックスが除去されていること
	if tasks[0].group != "my-app-rails" {
		t.Errorf("group = %q, want my-app-rails", tasks[0].group)
	}
}

func TestListActiveTasksBatching(t *testing.T) {
	// describeTasksBatchSize超のタスクが分割して問い合わせられることを確認する
	const total = describeTasksBatchSize + 50

	arns := make([]string, total)
	for i := range arns {
		arns[i] = "arn:aws:ecs:ap-northeast-1:1:task/c/task" + strings.Repeat("x", i%3)
	}

	var describeCalls int
	client := &mockECS{
		listTasks: func(in *ecs.ListTasksInput) (*ecs.ListTasksOutput, error) {
			return &ecs.ListTasksOutput{TaskArns: arns}, nil
		},
		describeTasks: func(in *ecs.DescribeTasksInput) (*ecs.DescribeTasksOutput, error) {
			describeCalls++
			if len(in.Tasks) > describeTasksBatchSize {
				t.Errorf("batch size %d exceeds limit %d", len(in.Tasks), describeTasksBatchSize)
			}
			var out ecs.DescribeTasksOutput
			for _, arn := range in.Tasks {
				out.Tasks = append(out.Tasks, types.Task{TaskArn: aws.String(arn), Group: aws.String("g")})
			}
			return &out, nil
		},
	}

	tasks, err := listActiveTasks(context.Background(), client, "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != total {
		t.Errorf("got %d tasks, want %d", len(tasks), total)
	}
	if describeCalls != 2 {
		t.Errorf("describeTasks called %d times, want 2", describeCalls)
	}
}
