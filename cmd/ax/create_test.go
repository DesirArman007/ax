// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/google/ax/pkg/apis/v1alpha1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
)

type mockAXClient struct {
	v1alpha1.AXClient
	createTaskFn      func(ctx context.Context, in *v1alpha1.CreateTaskRequest, opts ...grpc.CallOption) (*v1alpha1.Task, error)
	getWorkspaceFn    func(ctx context.Context, in *v1alpha1.GetWorkspaceRequest, opts ...grpc.CallOption) (*v1alpha1.Workspace, error)
	createWorkspaceFn func(ctx context.Context, in *v1alpha1.CreateWorkspaceRequest, opts ...grpc.CallOption) (*v1alpha1.Workspace, error)
}

func (m *mockAXClient) CreateTask(ctx context.Context, in *v1alpha1.CreateTaskRequest, opts ...grpc.CallOption) (*v1alpha1.Task, error) {
	if m.createTaskFn != nil {
		return m.createTaskFn(ctx, in, opts...)
	}
	return m.AXClient.CreateTask(ctx, in, opts...)
}
func (m *mockAXClient) GetWorkspace(ctx context.Context, in *v1alpha1.GetWorkspaceRequest, opts ...grpc.CallOption) (*v1alpha1.Workspace, error) {
	if m.getWorkspaceFn != nil {
		return m.getWorkspaceFn(ctx, in, opts...)
	}
	return m.AXClient.GetWorkspace(ctx, in, opts...)
}
func (m *mockAXClient) CreateWorkspace(ctx context.Context, in *v1alpha1.CreateWorkspaceRequest, opts ...grpc.CallOption) (*v1alpha1.Workspace, error) {
	if m.createWorkspaceFn != nil {
		return m.createWorkspaceFn(ctx, in, opts...)
	}
	return m.AXClient.CreateWorkspace(ctx, in, opts...)
}

func TestCreateDocument_CreateNewTask(t *testing.T) {
	manifestYAML := `
apiVersion: ax.io/v1alpha1
kind: Task
metadata:
  name: my-task
  atespace: default
spec:
  profile:
    image: ubuntu:latest
  commands: ["echo", "hello"]
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(manifestYAML), &node); err != nil {
		t.Fatalf("failed to parse YAML: %v", err)
	}

	created := false
	client := &mockAXClient{
		createTaskFn: func(ctx context.Context, in *v1alpha1.CreateTaskRequest, opts ...grpc.CallOption) (*v1alpha1.Task, error) {
			created = true
			return in.Task, nil
		},
	}

	docNode := node.Content[0]
	kind, name, err := createDocument(context.Background(), client, docNode, nil)
	if err != nil {
		t.Fatalf("expected createDocument to succeed, got: %v", err)
	}
	if !created {
		t.Errorf("expected CreateTask to be called")
	}
	if kind != "Task" || name != "my-task" {
		t.Errorf("expected Task/my-task created, got %s/%s", kind, name)
	}
}

func TestCreateDocument_ExistingTaskFailsImmutable(t *testing.T) {
	manifestYAML := `
apiVersion: ax.io/v1alpha1
kind: Task
metadata:
  name: existing-task
  atespace: default
spec:
  profile:
    image: ubuntu:latest
  commands: ["echo", "hello"]
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(manifestYAML), &node); err != nil {
		t.Fatalf("failed to parse YAML: %v", err)
	}

	client := &mockAXClient{
		createTaskFn: func(ctx context.Context, in *v1alpha1.CreateTaskRequest, opts ...grpc.CallOption) (*v1alpha1.Task, error) {
			return nil, status.Errorf(codes.FailedPrecondition, "task %s/%s already exists and is immutable", in.Task.GetMetadata().GetAtespace(), in.Task.GetMetadata().GetName())
		},
	}

	docNode := node.Content[0]
	_, _, err := createDocument(context.Background(), client, docNode, nil)
	if err == nil {
		t.Fatalf("expected error creating to existing task, got nil")
	}
	if !strings.Contains(err.Error(), "already exists and is immutable") {
		t.Errorf("createDocument() error = %v, want mentioning %q", err, "already exists and is immutable")
	}
}

func TestCreateDocument_Workspace(t *testing.T) {
	manifestYAML := `
apiVersion: ax.io/v1alpha1
kind: Workspace
metadata:
  name: dev-ws
  atespace: default
spec:
  profile:
    resources:
      limits:
        cpu: "2"
        memory: "4Gi"
  setup:
    git:
      - name: origin
        repo: https://github.com/org/repo.git
        ref: main
    commands:
      - make deps
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(manifestYAML), &node); err != nil {
		t.Fatalf("yaml.Unmarshal() failed: %v", err)
	}
	docNode := node.Content[0]

	t.Run("create", func(t *testing.T) {
		client := &mockAXClient{
			getWorkspaceFn: func(ctx context.Context, in *v1alpha1.GetWorkspaceRequest, opts ...grpc.CallOption) (*v1alpha1.Workspace, error) {
				return nil, status.Error(codes.NotFound, "not found")
			},
			createWorkspaceFn: func(ctx context.Context, in *v1alpha1.CreateWorkspaceRequest, opts ...grpc.CallOption) (*v1alpha1.Workspace, error) {
				if got := in.GetWorkspace().GetSpec().GetProfile().GetResources().GetLimits().GetCpu(); got != "2" {
					t.Errorf("Profile.Resources.Limits.Cpu = %q, want %q", got, "2")
				}
				if got := in.GetWorkspace().GetSpec().GetSetup().GetGit()[0].GetRef(); got != "main" {
					t.Errorf("Git.Ref = %q, want %q", got, "main")
				}
				return in.GetWorkspace(), nil
			},
		}
		kind, name, err := createDocument(t.Context(), client, docNode, nil)
		if err != nil {
			t.Fatalf("createDocument() failed: %v", err)
		}
		if kind != "Workspace" || name != "dev-ws" {
			t.Errorf("createDocument() = (%q, %q), want (\"Workspace\", \"dev-ws\")", kind, name)
		}
	})

	t.Run("existing_fails", func(t *testing.T) {
		client := &mockAXClient{
			getWorkspaceFn: func(ctx context.Context, in *v1alpha1.GetWorkspaceRequest, opts ...grpc.CallOption) (*v1alpha1.Workspace, error) {
				return &v1alpha1.Workspace{Metadata: &v1alpha1.ObjectMeta{Name: "dev-ws", Atespace: "default"}}, nil
			},
		}
		if _, _, err := createDocument(t.Context(), client, docNode, nil); status.Code(err) != codes.AlreadyExists {
			t.Fatalf("createDocument() error = %v, want AlreadyExists", err)
		}
	})
}

func TestPrintWorkspaceRow(t *testing.T) {
	var buf strings.Builder
	printWorkspaceRow(&buf, &v1alpha1.Workspace{
		Metadata: &v1alpha1.ObjectMeta{Name: "empty-phase", Atespace: "default"},
	})
	printWorkspaceRow(&buf, &v1alpha1.Workspace{
		Metadata: &v1alpha1.ObjectMeta{Name: "ready-ws", Atespace: "prod"},
		Spec: &v1alpha1.WorkspaceSpec{
			Setup: &v1alpha1.WorkspaceSetup{Git: []*v1alpha1.GitRepo{{Repo: "https://github.com/org/repo.git"}}},
			Mcp:   &v1alpha1.MCPConfig{Servers: []*v1alpha1.MCPServer{{Name: "s1"}, {Name: "s2"}}},
		},
		Status: &v1alpha1.WorkspaceStatus{Phase: v1alpha1.WorkspacePhaseReady},
	})
	want := "empty-phase\tdefault\t-\t0\t0\nready-ws\tprod\tReady\t1\t2\n"
	if got := buf.String(); got != want {
		t.Errorf("printWorkspaceRow() =\n%q\nwant:\n%q", got, want)
	}
}
