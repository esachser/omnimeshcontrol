package model

import "testing"

func TestParsePolicyAction(t *testing.T) {
	tests := []struct {
		raw     string
		want    PolicyAction
		wantErr bool
	}{
		{raw: "", want: PolicyActionAllow},
		{raw: "allow", want: PolicyActionAllow},
		{raw: "ALLOW", want: PolicyActionAllow},
		{raw: " deny ", want: PolicyActionDeny},
		{raw: "DENY", want: PolicyActionDeny},
		{raw: "dry_run", want: PolicyActionDryRun},
		{raw: "DRY_RUN", want: PolicyActionDryRun},
		{raw: "dry-run", want: PolicyActionDryRun},
		{raw: "dryrun", want: PolicyActionDryRun},
		{raw: "invalid", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := ParsePolicyAction(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParsePolicyAction(%q) err = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("ParsePolicyAction(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}

	if got := PolicyActionAllow.String(); got != "ALLOW" {
		t.Errorf("PolicyActionAllow.String() = %q, want ALLOW", got)
	}
	if got := PolicyActionDeny.String(); got != "DENY" {
		t.Errorf("PolicyActionDeny.String() = %q, want DENY", got)
	}
	if got := PolicyActionDryRun.String(); got != "DRY_RUN" {
		t.Errorf("PolicyActionDryRun.String() = %q, want DRY_RUN", got)
	}
}
