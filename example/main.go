// Package main shows how to build a CLI backed by an inline GraphQL schema.
//
// Replace the schema, resolver, and auth integration with your own gqlgen setup.
// Operations execute in-process — no HTTP server is needed.
//
// Commands added by this example:
//
//	query      Execute a GraphQL query
//	mutation   Execute a GraphQL mutation
//	describe   Show the SDL definition of a type
//	types      List all types in the schema
//	login      Authenticate and save a session token
//	logout     Clear the saved token
//	whoami     Show the currently authenticated user
package main

import (
	"context"
	"log"
	"os"

	"github.com/urfave/cli/v2"
	gqlcli "github.com/wricardo/gqlcli/pkg"

	"github.com/wricardo/gqlcli/example/graph"
)

func main() {
	// 1. Create your gqlgen ExecutableSchema.
	//    NewResolver() seeds a few example books.
	r := graph.NewResolver()
	execSchema := graph.NewExecutableSchema(graph.Config{Resolvers: r})

	// 2. Create a token store — tokens persist at ~/.myapp/token.
	tokens := gqlcli.NewTokenStore("myapp")

	// 3. Create an inline executor.
	//    WithContextEnricher injects dataloaders, user auth, etc. before each op.
	//    WithSchemaHints adds compact type SDL to validation error messages.
	exec := gqlcli.NewInlineExecutor(execSchema,
		gqlcli.WithSchemaHints(),
		gqlcli.WithContextEnricher(func(ctx context.Context) context.Context {
			// Load the saved token and inject your app's user context.
			token, err := tokens.Load()
			if err == nil && token != "" {
				claims, parseErr := tokens.ParseClaims(token)
				if parseErr == nil {
					// Replace with your app's auth injection, e.g.:
					//   ctx = auth.SetUser(ctx, auth.User{ID: claims.UserID})
					_ = claims
				}
			}
			// Inject dataloaders, feature flags, request-scoped state, etc.
			//   ctx = dataloader.NewLoaders(db).Attach(ctx)
			return ctx
		}),
	)

	// 4. Build CLI commands backed by the inline executor.
	commands := gqlcli.NewInlineCommandSet(exec,
		gqlcli.WithTokenStore(tokens),
		gqlcli.WithLogin(gqlcli.LoginConfig{
			// The GraphQL mutation that authenticates and returns a JWT.
			// Must accept $email and $password string variables.
			Mutation: `
				mutation Login($email: String!, $password: String!) {
					login(input: {email: $email, password: $password}) {
						token
					}
				}
			`,
			// Tell the library how to extract the token from the response.
			ExtractToken: func(data map[string]interface{}) (string, error) {
				login, _ := data["login"].(map[string]interface{})
				token, _ := login["token"].(string)
				return token, nil
			},
			Tokens: tokens,
		}),
	)

	// 5. Wire into a urfave/cli app alongside any app-specific commands.
	app := &cli.App{
		Name:    "myapp",
		Usage:   "CLI for my GraphQL API",
		Version: "0.1.0",
	}
	commands.Mount(app)

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
