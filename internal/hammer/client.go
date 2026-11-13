package hammer

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"cloud.google.com/go/spanner"
	database "cloud.google.com/go/spanner/admin/database/apiv1"
	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	"github.com/cloudspannerecosystem/memefish/ast"
	"google.golang.org/api/option"
)

type Client struct {
	database string
	client   *spanner.Client
	admin    *database.DatabaseAdminClient
}

func NewClient(ctx context.Context, uri string) (*Client, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	db := u.Host + u.Path

	opts := []option.ClientOption{}
	if credentials := u.Query().Get("credentials"); credentials != "" {
		opts = append(opts, option.WithCredentialsFile(credentials))
	}
	client, err := spanner.NewClient(ctx, db, opts...)
	if err != nil {
		return nil, err
	}
	admin, err := database.NewDatabaseAdminClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{
		database: db,
		client:   client,
		admin:    admin,
	}, nil
}

func (c *Client) Close() error {
	c.client.Close()
	return c.admin.Close()
}

func (c *Client) GetDatabaseDDL(ctx context.Context) (string, []byte, error) {
	response, err := c.admin.GetDatabaseDdl(ctx, &databasepb.GetDatabaseDdlRequest{
		Database: c.database,
	})
	if err != nil {
		return "", nil, err
	}
	return strings.Join(response.Statements, ";\n"), response.ProtoDescriptors, nil
}

func (c *Client) CreateDatabase(ctx context.Context, ddl DDL) error {
	descriptors, err := protoDescriptorsToSend(ddl.List, ddl.ProtoDescriptors)
	if err != nil {
		return err
	}
	parts := strings.Split(c.database, "/")
	stmts := make([]string, len(ddl.List))
	for i, stmt := range ddl.List {
		stmts[i] = stmt.SQL()
	}
	op, err := c.admin.CreateDatabase(ctx, &databasepb.CreateDatabaseRequest{
		Parent:           fmt.Sprintf("projects/%s/instances/%s", parts[1], parts[3]),
		CreateStatement:  fmt.Sprintf("CREATE DATABASE `%s`", parts[5]),
		ExtraStatements:  stmts,
		ProtoDescriptors: descriptors,
	})
	if err != nil {
		return err
	}
	_, err = op.Wait(ctx)
	return err
}

func (c *Client) ApplyDatabaseDDL(ctx context.Context, ddl DDL) error {
	if _, err := protoDescriptorsToSend(ddl.List, ddl.ProtoDescriptors); err != nil {
		return err
	}
	var stmts []Statement
	for _, stmt := range ddl.List {
		if c.isUpdateDatabaseStatement(stmt) {
			stmts = append(stmts, stmt)
		} else {
			if len(stmts) > 0 {
				if err := c.updateDatabaseDDL(ctx, stmts, ddl.ProtoDescriptors); err != nil {
					return err
				}
				stmts = stmts[:0]
			}
			if err := c.partitionedUpdate(ctx, stmt.SQL()); err != nil {
				return err
			}
		}
	}
	if len(stmts) > 0 {
		if err := c.updateDatabaseDDL(ctx, stmts, ddl.ProtoDescriptors); err != nil {
			return err
		}
	}
	return nil
}

// updateDatabaseDDL sends the descriptors only with a batch that has a bundle statement.
func (c *Client) updateDatabaseDDL(ctx context.Context, stmts []Statement, descriptors []byte) error {
	sql := make([]string, len(stmts))
	for i, stmt := range stmts {
		sql[i] = stmt.SQL()
	}
	descriptors, err := protoDescriptorsToSend(stmts, descriptors)
	if err != nil {
		return err
	}
	op, err := c.admin.UpdateDatabaseDdl(ctx, &databasepb.UpdateDatabaseDdlRequest{
		Database:         c.database,
		Statements:       sql,
		ProtoDescriptors: descriptors,
	})
	if err != nil {
		return err
	}
	return op.Wait(ctx)
}

func (c *Client) partitionedUpdate(ctx context.Context, stmt string) error {
	_, err := c.client.PartitionedUpdate(ctx, spanner.Statement{SQL: stmt})
	return err
}

func (c *Client) isUpdateDatabaseStatement(stmt Statement) bool {
	switch stmt.(type) {
	case Update:
		return false
	default:
		return true
	}
}

// protoDescriptorsToSend returns the descriptors if a statement is a proto bundle statement, else nil.
// DROP PROTO BUNDLE needs none. Spanner reads a deleted type's definition from them, so a DELETE
// of a type they lack fails here instead of with an opaque NotFound.
func protoDescriptorsToSend(stmts []Statement, descriptors []byte) ([]byte, error) {
	var send bool
	for _, stmt := range stmts {
		if dl, ok := stmt.(DataLoss); ok {
			stmt = dl.Statement
		}
		switch stmt := stmt.(type) {
		case *ast.DropProtoBundle:
			send = true
		case *ast.CreateProtoBundle, *ast.AlterProtoBundle:
			if len(descriptors) == 0 {
				return nil, fmt.Errorf("%s needs proto descriptors; pass --proto-descriptors", stmt.SQL())
			}
			send = true
		}
		if alter, ok := stmt.(*ast.AlterProtoBundle); ok && alter.Delete != nil {
			types, err := parseProtoTypes(descriptors)
			if err != nil {
				return nil, err
			}
			for _, nt := range alter.Delete.Types.Types {
				if !types[identsToComparable(nt.Path...)].exists() {
					return nil, fmt.Errorf("%s is not in the proto descriptors, which Spanner needs to delete it: keep it in them until the DELETE is applied, then remove it in a later migration", nt.SQL())
				}
			}
		}
	}
	if send {
		return descriptors, nil
	}
	return nil, nil
}
