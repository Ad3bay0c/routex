package agents

import "testing"

func TestParseRestartPolicy(t *testing.T) {
	tests := []struct {
		input   string
		want    RestartPolicy
		wantErr bool
	}{
		{"one_for_one", OneForOne, false},
		{"one_for_all", OneForAll, false},
		{"rest_for_one", RestForOne, false},
		{"", OneForOne, false}, // empty defaults to one_for_one
		{"invalid", "", true},  // unknown value is an error
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseRestartPolicy(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseRestartPolicy(%q) expected error, got nil", tt.input)
				}
				return
			}

			if err != nil {
				t.Errorf("ParseRestartPolicy(%q) unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("ParseRestartPolicy(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRestartPolicy_String(t *testing.T) {
	tests := []struct {
		name  string
		input RestartPolicy
		want  string
	}{
		{"oneForOne", OneForOne, "one_for_one"},
		{"oneForAll", OneForAll, "one_for_all"},
		{"restForOne", RestForOne, "rest_for_one"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.input.String()
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
