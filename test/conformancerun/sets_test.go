//go:build test

package main

import (
	"reflect"
	"testing"
)

func TestSelectSets(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    []string
		wantErr bool
	}{
		{name: "default leaves out auth", args: nil, want: []string{"agent", "cli", "legacy"}},
		{name: "auth on request", args: []string{"auth"}, want: []string{"auth"}},
		{name: "unknown set", args: []string{"server"}, wantErr: true},
		{name: "two sets", args: []string{"agent", "cli"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selected, err := selectSets(test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("selectSets(%v) error = %v, want error %v", test.args, err, test.wantErr)
			}
			var names []string
			for _, set := range selected {
				names = append(names, set.Name)
			}
			if !reflect.DeepEqual(names, test.want) {
				t.Fatalf("selectSets(%v) = %v, want %v", test.args, names, test.want)
			}
		})
	}
}
