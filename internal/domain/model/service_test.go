package model

import (
	"reflect"
	"testing"
)

func TestService_EffectiveSelectorSets(t *testing.T) {
	tests := []struct {
		name string
		svc  Service
		want [][]string
	}{
		{
			name: "no selectors configured",
			svc:  Service{},
			want: nil,
		},
		{
			name: "legacy flat selectors treated as single set",
			svc: Service{
				Selectors: []string{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
			},
			want: [][]string{
				{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
			},
		},
		{
			name: "multiple selector sets for windows and linux",
			svc: Service{
				SelectorSets: [][]string{
					{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
					{"unix:user:payment", "unix:pid:1234"},
				},
			},
			want: [][]string{
				{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
				{"unix:user:payment", "unix:pid:1234"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.svc.EffectiveSelectorSets()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("EffectiveSelectorSets() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestService_ValidateSelectors(t *testing.T) {
	tests := []struct {
		name    string
		svc     Service
		wantErr bool
	}{
		{
			name:    "empty is valid",
			svc:     Service{},
			wantErr: false,
		},
		{
			name: "valid legacy selectors",
			svc: Service{
				Selectors: []string{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
			},
			wantErr: false,
		},
		{
			name: "valid multiple selector sets",
			svc: Service{
				SelectorSets: [][]string{
					{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
					{"unix:user:payment", "unix:pid:1234"},
				},
			},
			wantErr: false,
		},
		{
			name: "empty inner set",
			svc: Service{
				SelectorSets: [][]string{
					{"windows:user_sid:S-1-5-21-1"},
					{},
				},
			},
			wantErr: true,
		},
		{
			name: "blank selector inside set",
			svc: Service{
				SelectorSets: [][]string{
					{"windows:user_sid:S-1-5-21-1", "   "},
				},
			},
			wantErr: true,
		},
		{
			name: "blank legacy selector",
			svc: Service{
				Selectors: []string{"unix:user:app", ""},
			},
			wantErr: true,
		},
		{
			name: "both selectors and selectorSets set",
			svc: Service{
				Selectors:    []string{"unix:user:app"},
				SelectorSets: [][]string{{"windows:user_sid:S-1-5-21-1"}},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.svc.ValidateSelectors()
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateSelectors() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestServiceIdentity_EffectiveSelectorSets(t *testing.T) {
	id := ServiceIdentity{
		SPIFFEID:  "spiffe://omnimesh.local/ns/prod/sa/api",
		ServiceID: ServiceID{Namespace: "prod", Name: "api"},
		SelectorSets: [][]string{
			{"windows:user_sid:S-1-5-21-1", "windows:sha256:abc"},
			{"unix:uid:1000", "unix:pid:42"},
		},
	}

	got := id.EffectiveSelectorSets()
	if len(got) != 2 {
		t.Fatalf("EffectiveSelectorSets() len = %d, want 2", len(got))
	}
	if !reflect.DeepEqual(got, id.SelectorSets) {
		t.Errorf("EffectiveSelectorSets() = %v, want %v", got, id.SelectorSets)
	}
}
