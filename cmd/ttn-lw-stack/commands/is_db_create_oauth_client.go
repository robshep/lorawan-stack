// Copyright © 2019 The Things Network Foundation, The Things Industries B.V.
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

package commands

import (
	"context"
	"time"

	"github.com/spf13/cobra"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"go.thethings.network/lorawan-stack/v3/pkg/auth"
	bunstore "go.thethings.network/lorawan-stack/v3/pkg/identityserver/bunstore"
	"go.thethings.network/lorawan-stack/v3/pkg/identityserver/store"
	"go.thethings.network/lorawan-stack/v3/pkg/ttnpb"
	storeutil "go.thethings.network/lorawan-stack/v3/pkg/util/store"
)

var createOAuthClient = &cobra.Command{
	Use:   "create-oauth-client",
	Short: "Create an OAuth client in the Identity Server database",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		logger.Info("Connecting to Identity Server database...")
		db, err := storeutil.OpenDB(ctx, config.IS.DatabaseURI)
		if err != nil {
			return err
		}
		bunDB := bun.NewDB(db, pgdialect.New())
		st, err := bunstore.NewStore(ctx, bunDB)
		if err != nil {
			return err
		}
		defer db.Close()

		clientID, err := cmd.Flags().GetString("id")
		if err != nil {
			return err
		}
		name, err := cmd.Flags().GetString("name")
		if err != nil {
			return err
		}
		owner, err := cmd.Flags().GetString("owner")
		if err != nil {
			return err
		}
		secret, err := cmd.Flags().GetString("secret")
		if err != nil {
			return err
		}
		if secret == "" {
			noSecret, err := cmd.Flags().GetBool("no-secret")
			if err != nil {
				return err
			}
			if !noSecret {
				secret, err = auth.GenerateKey(ctx)
				if err != nil {
					return err
				}
			}
		}
		var hashedSecret string
		if secret != "" {
			hashedSecret, err = auth.Hash(ctx, secret)
			if err != nil {
				return err
			}
		}
		redirectURIs, err := cmd.Flags().GetStringSlice("redirect-uri")
		if err != nil {
			return err
		}
		logoutRedirectURIs, err := cmd.Flags().GetStringSlice("logout-redirect-uri")
		if err != nil {
			return err
		}
		authorized, err := cmd.Flags().GetBool("authorized")
		if err != nil {
			return err
		}
		endorsed, err := cmd.Flags().GetBool("endorsed")
		if err != nil {
			return err
		}

		cliFieldMask := []string{
			"name",
			"secret",
			"redirect_uris",
			"logout_redirect_uris",
			"state",
			"skip_authorization",
			"endorsed",
			"grants",
			"rights",
		}
		cli := &ttnpb.Client{
			Ids: &ttnpb.ClientIdentifiers{ClientId: clientID},
		}

		err = st.Transact(ctx, func(ctx context.Context, st store.Store) error {
			var cliExists bool
			if _, err := st.GetClient(ctx, cli.GetIds(), cliFieldMask); err == nil {
				cliExists = true
			}
			cli.Name = name
			cli.Secret = hashedSecret
			cli.RedirectUris = redirectURIs
			cli.LogoutRedirectUris = logoutRedirectURIs
			cli.State = ttnpb.State_STATE_APPROVED
			cli.SkipAuthorization = authorized
			cli.Endorsed = endorsed
			cli.Grants = []ttnpb.GrantType{
				ttnpb.GrantType_GRANT_AUTHORIZATION_CODE,
				ttnpb.GrantType_GRANT_REFRESH_TOKEN,
			}
			cli.Rights = []ttnpb.Right{ttnpb.Right_RIGHT_ALL}

			if cliExists {
				logger.Info("Updating OAuth client...")
				if _, err = st.UpdateClient(ctx, cli, cliFieldMask); err != nil {
					return err
				}
				logger.WithField("secret", secret).Info("Updated OAuth client")
			} else {
				logger.Info("Creating OAuth client...")
				if _, err = st.CreateClient(ctx, cli); err != nil {
					return err
				}
				logger.WithField("secret", secret).Info("Created OAuth client")
			}

			if owner != "" {
				logger.Info("Setting owner rights...")
				err = st.SetMember(
					ctx,
					(&ttnpb.UserIdentifiers{UserId: owner}).GetOrganizationOrUserIdentifiers(),
					cli.GetIds().GetEntityIdentifiers(),
					ttnpb.RightsFrom(ttnpb.Right_RIGHT_CLIENT_ALL),
				)
				if err != nil {
					return err
				}
				logger.Info("Set owner rights")
			}
			return nil
		})

		if err != nil {
			return err
		}

		return nil
	},
}

func init() {
	createOAuthClient.Flags().String("id", "console", "OAuth client ID")
	createOAuthClient.Flags().String("name", "", "Name of the OAuth client")
	createOAuthClient.Flags().String("owner", "", "Owner of the OAuth client")
	createOAuthClient.Flags().String("secret", "", "Secret of the OAuth client")
	createOAuthClient.Flags().Bool("no-secret", false, "Do not generate a secret for the OAuth client")
	createOAuthClient.Flags().StringSlice("redirect-uri", []string{}, "Redirect URIs of the OAuth client")
	createOAuthClient.Flags().StringSlice("logout-redirect-uri", []string{}, "Logout redirect URIs of the OAuth client")
	createOAuthClient.Flags().Bool("authorized", true, "Mark OAuth client as pre-authorized")
	createOAuthClient.Flags().Bool("endorsed", true, "Mark OAuth client as endorsed ")
	isDBCommand.AddCommand(createOAuthClient)
}
