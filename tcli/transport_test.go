package tcli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"

	"github.com/nexssp/transport/codec"
	"github.com/nexssp/transport/tcli"
)

func TestTransport_OutputFormatting(t *testing.T) {
	t.Parallel()

	t.Run("Outputs plain string directly", func(t *testing.T) {
		stringAct := action.New("test.string", func(_ context.Context, _ struct{}) (string, error) {
			return "Plain Text Result", nil
		}).Route(tcli.Command("str-cmd", "String")).Build()

		var stdout, stderr bytes.Buffer
		cli := tcli.New(tcli.WithArgs("str-cmd"), tcli.WithOutput(&stdout, &stderr))
		cli.Mount([]action.AnyAction{stringAct})

		_, err := cli.Do(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(stdout.String()) != "Plain Text Result" {
			t.Fatalf("unexpected stdout: %q", stdout.String())
		}
	})

	t.Run("Outputs action.MessageRes unwrapped", func(t *testing.T) {
		msgAct := action.New("test.msg", func(_ context.Context, _ struct{}) (action.MessageRes, error) {
			return action.MessageRes{Message: "Operation Completed Successfully"}, nil
		}).Route(tcli.Command("msg-cmd", "Message")).Build()

		var stdout, stderr bytes.Buffer
		cli := tcli.New(tcli.WithArgs("msg-cmd"), tcli.WithOutput(&stdout, &stderr))
		cli.Mount([]action.AnyAction{msgAct})

		_, err := cli.Do(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(stdout.String()) != "Operation Completed Successfully" {
			t.Fatalf("unexpected stdout: %q", stdout.String())
		}
	})

	t.Run("Outputs struct as formatted JSON with Default/JSON codec", func(t *testing.T) {
		type DataRes struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		}
		structAct := action.New("test.struct", func(_ context.Context, _ struct{}) (DataRes, error) {
			return DataRes{ID: 101, Title: "Nexss Architect"}, nil
		}).Route(tcli.Command("json-cmd", "JSON Struct")).Build()

		var stdout, stderr bytes.Buffer
		cli := tcli.New(
			tcli.WithArgs("json-cmd"),
			tcli.WithCodec(codec.JSON{}),
			tcli.WithOutput(&stdout, &stderr),
		)
		cli.Mount([]action.AnyAction{structAct})

		_, err := cli.Do(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}

		var parsed DataRes
		if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
			t.Fatalf("failed parsing stdout as JSON: %v\nOutput: %s", err, stdout.String())
		}
		if parsed.ID != 101 || parsed.Title != "Nexss Architect" {
			t.Fatalf("unexpected parsed JSON: %+v", parsed)
		}
	})
}

func TestTransport_ActionExecutionFailure(t *testing.T) {
	t.Parallel()

	failingAct := action.New("test.failing", func(_ context.Context, _ struct{}) (string, error) {
		return "", xerr.BadRequest("missing required parameter")
	}).Route(tcli.Command("fail", "Failing action")).Build()

	var stdout, stderr bytes.Buffer
	cli := tcli.New(
		tcli.WithArgs("fail"),
		tcli.WithOutput(&stdout, &stderr),
	)
	cli.Mount([]action.AnyAction{failingAct})

	_, err := cli.Do(context.Background(), nil)
	if err == nil {
		t.Fatal("expected action failure error, got nil")
	}

	var appErr *xerr.AppError
	if !errors.As(err, &appErr) || appErr.Kind != xerr.KindBadRequest {
		t.Fatalf("expected KindBadRequest error, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "missing required parameter") {
		t.Fatalf("expected error output on stderr, got:\n%s", stderr.String())
	}
}

func TestTransport_DuplicateCommandBindingsAreRejected(t *testing.T) {
	first := action.New("module_a.pipeline.pack", func(_ context.Context, _ struct{}) (string, error) {
		return "first", nil
	}).Route(tcli.Command("pack", "First package command")).Build()
	second := action.New("module_b.pipeline.pack", func(_ context.Context, _ struct{}) (string, error) {
		return "second", nil
	}).Route(tcli.Command("pack", "Second package command")).Build()

	var stdout, stderr bytes.Buffer
	cli := tcli.New(tcli.WithArgs("pack"), tcli.WithOutput(&stdout, &stderr))
	cli.Mount([]action.AnyAction{first, second})

	_, err := cli.Do(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("Do() error = %v, want duplicate-command conflict", err)
	}
	if kind := xerr.KindFrom(err); kind != xerr.KindConflict {
		t.Fatalf("KindFrom(error) = %v, want %v", kind, xerr.KindConflict)
	}
	if !strings.Contains(err.Error(), "module_a.pipeline.pack") || !strings.Contains(err.Error(), "module_b.pipeline.pack") {
		t.Fatalf("conflict error does not identify both actions: %v", err)
	}
}

func TestTransport_DuplicateCommandBindingsAreReportedBeforeHelp(t *testing.T) {
	first := action.New("same.action", func(_ context.Context, _ struct{}) (string, error) {
		return "first", nil
	}).Route(tcli.Command("pack", "First package command")).Build()
	second := action.New("same.action", func(_ context.Context, _ struct{}) (string, error) {
		return "second", nil
	}).Route(tcli.Command("pack", "Second package command")).Build()

	var stdout, stderr bytes.Buffer
	cli := tcli.New(tcli.WithArgs(), tcli.WithOutput(&stdout, &stderr))
	cli.Mount([]action.AnyAction{first, second})

	_, err := cli.Do(context.Background(), nil)
	if err == nil || xerr.KindFrom(err) != xerr.KindConflict {
		t.Fatalf("Do() error = %v, want duplicate-command conflict", err)
	}
	if !strings.Contains(stderr.String(), "command \"pack\" is already bound") {
		t.Fatalf("stderr = %q, want explicit duplicate-command diagnostic", stderr.String())
	}
	if strings.Contains(stderr.String(), "Available Commands:") {
		t.Fatalf("duplicate commands should fail before help is rendered: %q", stderr.String())
	}
}

func TestTransport_NilSafetyAndEmptyArgs(t *testing.T) {
	t.Parallel()

	cli := tcli.New(nil, tcli.WithCodec(nil))
	if cli == nil {
		t.Fatal("expected non-nil transport instance")
	}

	var stdout, stderr bytes.Buffer
	cli = tcli.New(tcli.WithArgs(), tcli.WithOutput(&stdout, &stderr))
	res, err := cli.Do(context.Background(), nil)
	if err != nil || res != nil {
		t.Fatalf("expected nil result and error on empty args, got res=%v, err=%v", res, err)
	}
	if !strings.Contains(stderr.String(), "Available Commands:") {
		t.Fatalf("expected Available Commands in stderr:\n%s", stderr.String())
	}

	if cli.String() != "cli" {
		t.Fatalf("expected String() 'cli', got %q", cli.String())
	}
}
