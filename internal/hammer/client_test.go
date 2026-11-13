package hammer

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestProtoDescriptorsToSend(t *testing.T) {
	bytes := []byte{1} // opaque: only its presence matters here
	tests := []struct {
		name        string
		sql         string
		descriptors []byte
		want        []byte
		wantErr     string
	}{
		{"no bundle statement", "ALTER TABLE t ADD COLUMN c INT64", bytes, nil, ""},
		{"create", "CREATE PROTO BUNDLE (pkg.Msg)", bytes, bytes, ""},
		{"create without descriptors", "CREATE PROTO BUNDLE (pkg.Msg)", nil, nil, "needs proto descriptors"},
		{"drop needs none", "DROP PROTO BUNDLE", nil, nil, ""},
		{"insert without descriptors", "ALTER PROTO BUNDLE INSERT (pkg.Msg)", nil, nil, "needs proto descriptors"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ddl, err := ParseDDL("", tt.sql, &DDLOption{})
			if err != nil {
				t.Fatal(err)
			}
			got, err := protoDescriptorsToSend(ddl.List, tt.descriptors)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || string(got) != string(tt.want) {
				t.Errorf("got %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestProtoDescriptorsToSend_DataLossUpdate(t *testing.T) {
	ddl, err := ParseDDL("", "ALTER PROTO BUNDLE UPDATE (pkg.Msg)", &DDLOption{})
	if err != nil {
		t.Fatal(err)
	}
	stmts := []Statement{DataLoss{Statement: ddl.List[0], Reason: "r"}}
	if got, err := protoDescriptorsToSend(stmts, []byte{1}); err != nil || len(got) == 0 {
		t.Errorf("got %v, %v; want the descriptors", got, err)
	}
}

func TestProtoDescriptorsToSend_DeleteOfAbsentType(t *testing.T) {
	descriptors, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Package:     proto.String("pkg"),
		MessageType: []*descriptorpb.DescriptorProto{message("Msg")},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	for sql, wantErr := range map[string]string{
		"ALTER PROTO BUNDLE DELETE (pkg.Msg)":                                       "",
		"ALTER PROTO BUNDLE DELETE (pkg.Kind)":                                      "pkg.Kind is not in the proto descriptors",
		"ALTER PROTO BUNDLE INSERT (pkg.Msg); ALTER PROTO BUNDLE DELETE (pkg.Kind)": "pkg.Kind is not in the proto descriptors",
	} {
		ddl, err := ParseDDL("", sql, &DDLOption{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = protoDescriptorsToSend(ddl.List, descriptors)
		if (wantErr == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%s: err = %v, want %q", sql, err, wantErr)
		}
	}
}
