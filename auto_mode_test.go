package monkeyscode

import (
	"errors"
	"testing"
)

const gatedResult = `{"type":"result","subtype":"error_quota","is_error":true,"result":"","session_id":"s1","num_turns":1,"duration_ms":1,"usage":{},"cost_usd":0,"is_estimated":false,"permission_denials":[],"exit_code":4,"error":"x","plan_required":{"code":"auto_requires_paid_plan","title":"Auto mode needs a paid plan","message":"Auto mode is part of MonkeysCode Pro and above.","account_state":"trial_active","required_plan":"pro","upgrade_url":"/dashboard/billing?plan=pro&ref=auto_mode","fallback_model":"capuchin-reason","fallback_label":"Continue with Capuchin"}}`

func TestAutoPlanRequiredError(t *testing.T) {
	ev, err := ParseCLIEvent([]byte(gatedResult))
	if err != nil || ev.Result == nil || ev.Result.PlanRequired == nil {
		t.Fatalf("parse: %v %+v", err, ev.Result)
	}
	err = RequireSuccess(ev.Result)
	var pr *PlanRequiredError
	if !errors.As(err, &pr) {
		t.Fatalf("want *PlanRequiredError, got %T %v", err, err)
	}
	// Existing *ProcessError checks keep matching it.
	var pe *ProcessError
	if !errors.As(err, &pe) || pe.ExitCode != 4 {
		t.Fatalf("want *ProcessError exit 4 via Unwrap, got %+v", pe)
	}
	if pr.Plan.FallbackModel != "capuchin-reason" || pr.Plan.RequiredPlan != "pro" {
		t.Fatalf("plan fields: %+v", pr.Plan)
	}
	if got := pr.UpgradeURL(); got != "https://monkeyscode.com/dashboard/billing?plan=pro&ref=auto_mode" {
		t.Fatalf("upgrade url %q", got)
	}
	if pr.Error() != "Auto mode needs a paid plan: Auto mode is part of MonkeysCode Pro and above." {
		t.Fatalf("message %q", pr.Error())
	}
}

func TestAutoRequireSuccessOtherResults(t *testing.T) {
	ok := &CLIResult{Subtype: "success"}
	if err := RequireSuccess(ok); err != nil {
		t.Fatalf("success: %v", err)
	}
	other := &CLIResult{Subtype: "error_auth", IsError: true, ExitCode: 3, Error: "auth"}
	err := RequireSuccess(other)
	var pr *PlanRequiredError
	var pe *ProcessError
	if errors.As(err, &pr) || !errors.As(err, &pe) || pe.ExitCode != 3 {
		t.Fatalf("auth error: %T %v", err, err)
	}
}

func TestAutoRouteEvent(t *testing.T) {
	ev, err := ParseCLIEvent([]byte(`{"type":"system","subtype":"route","session_id":"s1","requested":"monkeyscode-auto","routed":"frontier-x","reason":"test_fail","phase":"execute","escalations_used":1}`))
	if err != nil || ev.Route == nil {
		t.Fatalf("route: %v %+v", err, ev)
	}
	if ev.Route.Routed != "frontier-x" || ev.Route.EscalationsUsed != 1 || ev.Route.Phase != "execute" {
		t.Fatalf("route fields: %+v", ev.Route)
	}
	// Other system events carry no Route.
	ev, _ = ParseCLIEvent([]byte(`{"type":"system","subtype":"retrying","session_id":"s1"}`))
	if ev.Route != nil {
		t.Fatal("retrying got a Route")
	}
	// A malformed route line keeps the event and skips Route.
	ev, err = ParseCLIEvent([]byte(`{"type":"system","subtype":"route","session_id":"s1","routed":7}`))
	if err != nil || ev.Route != nil || ev.Subtype != "route" {
		t.Fatalf("malformed: %v %+v", err, ev)
	}
}
