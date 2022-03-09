// Copyright © 2022 The Things Network Foundation, The Things Industries B.V.
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

package identityserver

import (
	"testing"

	pbtypes "github.com/gogo/protobuf/types"
	"github.com/smartystreets/assertions/should"
	"go.thethings.network/lorawan-stack/v3/pkg/errors"
	"go.thethings.network/lorawan-stack/v3/pkg/identityserver/storetest"
	"go.thethings.network/lorawan-stack/v3/pkg/ttnpb"
	"go.thethings.network/lorawan-stack/v3/pkg/util/test"
	"google.golang.org/grpc"
)

func init() {
	// remove applications assigned to the user by the populator
	userID := paginationUser.GetIds()
	for _, app := range population.Applications {
		for id, collaborators := range population.Memberships {
			if app.IDString() == id.IDString() {
				for i, collaborator := range collaborators {
					if collaborator.IDString() == userID.GetUserId() {
						collaborators = collaborators[:i+copy(collaborators[i:], collaborators[i+1:])]
					}
				}
			}
		}
	}

	// add deterministic number of applications
	for i := 0; i < 3; i++ {
		applicationID := population.Applications[i].GetEntityIdentifiers()
		population.Memberships[applicationID] = append(population.Memberships[applicationID], &ttnpb.Collaborator{
			Ids:    paginationUser.OrganizationOrUserIdentifiers(),
			Rights: []ttnpb.Right{ttnpb.Right_RIGHT_APPLICATION_ALL},
		})
	}
}

func TestApplicationsPermissionDenied(t *testing.T) {
	p := &storetest.Population{}
	usr1 := p.NewUser()
	app1 := p.NewApplication(usr1.GetOrganizationOrUserIdentifiers())

	t.Parallel()
	a, ctx := test.New(t)

	testWithIdentityServer(t, func(_ *IdentityServer, cc *grpc.ClientConn) {
		reg := ttnpb.NewApplicationRegistryClient(cc)

		_, err := reg.Create(ctx, &ttnpb.CreateApplicationRequest{
			Application: &ttnpb.Application{
				Ids: &ttnpb.ApplicationIdentifiers{ApplicationId: "foo-app"},
			},
			Collaborator: usr1.GetOrganizationOrUserIdentifiers(),
		})
		if a.So(err, should.NotBeNil) {
			a.So(errors.IsPermissionDenied(err), should.BeTrue)
		}

		_, err = reg.Get(ctx, &ttnpb.GetApplicationRequest{
			ApplicationIds: app1.GetIds(),
			FieldMask:      &pbtypes.FieldMask{Paths: []string{"name"}},
		})
		if a.So(err, should.NotBeNil) {
			a.So(errors.IsUnauthenticated(err), should.BeTrue)
		}

		listRes, err := reg.List(ctx, &ttnpb.ListApplicationsRequest{
			FieldMask: &pbtypes.FieldMask{Paths: []string{"name"}},
		})
		a.So(err, should.BeNil)
		if a.So(listRes, should.NotBeNil) {
			a.So(listRes.Applications, should.BeEmpty)
		}

		_, err = reg.List(ctx, &ttnpb.ListApplicationsRequest{
			Collaborator: usr1.GetOrganizationOrUserIdentifiers(),
			FieldMask:    &pbtypes.FieldMask{Paths: []string{"name"}},
		})
		if a.So(err, should.NotBeNil) {
			a.So(errors.IsPermissionDenied(err), should.BeTrue)
		}

		_, err = reg.Update(ctx, &ttnpb.UpdateApplicationRequest{
			Application: &ttnpb.Application{
				Ids:  app1.GetIds(),
				Name: "Updated Name",
			},
			FieldMask: &pbtypes.FieldMask{Paths: []string{"name"}},
		})
		if a.So(err, should.NotBeNil) {
			a.So(errors.IsPermissionDenied(err), should.BeTrue)
		}

		_, err = reg.Delete(ctx, app1.GetIds())
		if a.So(err, should.NotBeNil) {
			a.So(errors.IsPermissionDenied(err), should.BeTrue)
		}
	}, withPrivateTestDatabase(p))
}

func TestApplicationsCRUD(t *testing.T) {
	p := &storetest.Population{}

	adminUsr := p.NewUser()
	adminUsr.Admin = true
	adminKey, _ := p.NewAPIKey(adminUsr.GetEntityIdentifiers(), ttnpb.Right_RIGHT_ALL)
	adminCreds := rpcCreds(adminKey)

	usr1 := p.NewUser()
	for i := 0; i < 5; i++ {
		p.NewApplication(usr1.GetOrganizationOrUserIdentifiers())
	}

	usr2 := p.NewUser()
	for i := 0; i < 5; i++ {
		p.NewApplication(usr2.GetOrganizationOrUserIdentifiers())
	}

	key, _ := p.NewAPIKey(usr1.GetEntityIdentifiers(), ttnpb.Right_RIGHT_ALL)
	creds := rpcCreds(key)
	keyWithoutRights, _ := p.NewAPIKey(usr1.GetEntityIdentifiers())
	credsWithoutRights := rpcCreds(keyWithoutRights)

	t.Parallel()
	a, ctx := test.New(t)

	testWithIdentityServer(t, func(is *IdentityServer, cc *grpc.ClientConn) {
		reg := ttnpb.NewApplicationRegistryClient(cc)

		// Test batch fetch with cluster authorization
		list, err := reg.List(ctx, &ttnpb.ListApplicationsRequest{
			FieldMask:    &pbtypes.FieldMask{Paths: []string{"ids"}},
			Collaborator: nil,
			Deleted:      true,
		}, is.WithClusterAuth())
		if a.So(err, should.BeNil) {
			a.So(list.Applications, should.HaveLength, 10)
		}

		is.config.UserRights.CreateApplications = false

		_, err = reg.Create(ctx, &ttnpb.CreateApplicationRequest{
			Application: &ttnpb.Application{
				Ids:  &ttnpb.ApplicationIdentifiers{ApplicationId: "foo"},
				Name: "Foo Application",
			},
			Collaborator: usr1.GetOrganizationOrUserIdentifiers(),
		}, creds)
		if a.So(err, should.NotBeNil) {
			a.So(errors.IsPermissionDenied(err), should.BeTrue)
		}

		is.config.UserRights.CreateApplications = true

		created, err := reg.Create(ctx, &ttnpb.CreateApplicationRequest{
			Application: &ttnpb.Application{
				Ids:  &ttnpb.ApplicationIdentifiers{ApplicationId: "foo"},
				Name: "Foo Application",
			},
			Collaborator: usr1.GetOrganizationOrUserIdentifiers(),
		}, creds)
		if a.So(err, should.BeNil) && a.So(created, should.NotBeNil) {
			a.So(created.Name, should.Equal, "Foo Application")
		}

		got, err := reg.Get(ctx, &ttnpb.GetApplicationRequest{
			ApplicationIds: created.GetIds(),
			FieldMask:      &pbtypes.FieldMask{Paths: []string{"name"}},
		}, creds)
		if a.So(err, should.BeNil) && a.So(got, should.NotBeNil) {
			a.So(got.Name, should.Equal, created.Name)
		}

		got, err = reg.Get(ctx, &ttnpb.GetApplicationRequest{
			ApplicationIds: created.GetIds(),
			FieldMask:      &pbtypes.FieldMask{Paths: []string{"ids"}},
		}, credsWithoutRights)
		a.So(err, should.BeNil)

		got, err = reg.Get(ctx, &ttnpb.GetApplicationRequest{
			ApplicationIds: created.GetIds(),
			FieldMask:      &pbtypes.FieldMask{Paths: []string{"attributes"}},
		}, credsWithoutRights)
		if a.So(err, should.NotBeNil) {
			a.So(errors.IsPermissionDenied(err), should.BeTrue)
		}

		updated, err := reg.Update(ctx, &ttnpb.UpdateApplicationRequest{
			Application: &ttnpb.Application{
				Ids:  created.GetIds(),
				Name: "Updated Name",
			},
			FieldMask: &pbtypes.FieldMask{Paths: []string{"name"}},
		}, creds)
		if a.So(err, should.BeNil) && a.So(updated, should.NotBeNil) {
			a.So(updated.Name, should.Equal, "Updated Name")
		}

		for _, collaborator := range []*ttnpb.OrganizationOrUserIdentifiers{nil, usr1.OrganizationOrUserIdentifiers()} {
			list, err := reg.List(ctx, &ttnpb.ListApplicationsRequest{
				FieldMask:    &pbtypes.FieldMask{Paths: []string{"name"}},
				Collaborator: collaborator,
			}, creds)
			if a.So(err, should.BeNil) && a.So(list, should.NotBeNil) && a.So(list.Applications, should.HaveLength, 6) {
				var found bool
				for _, item := range list.Applications {
					if item.GetIds().GetApplicationId() == created.GetIds().GetApplicationId() {
						found = true
						a.So(item.Name, should.Equal, updated.Name)
					}
				}
				a.So(found, should.BeTrue)
			}
		}

		_, err = reg.Delete(ctx, created.GetIds(), creds)
		a.So(err, should.BeNil)

		_, err = reg.Purge(ctx, created.GetIds(), creds)
		if a.So(err, should.NotBeNil) {
			a.So(errors.IsPermissionDenied(err), should.BeTrue)
		}

		_, err = reg.Purge(ctx, created.GetIds(), adminCreds)
		a.So(err, should.BeNil)
	}, withPrivateTestDatabase(p))
}

func TestApplicationsPagination(t *testing.T) {
	a, ctx := test.New(t)

	testWithIdentityServer(t, func(is *IdentityServer, cc *grpc.ClientConn) {
		userID := paginationUser.GetIds()
		creds := userCreds(paginationUserIdx)

		reg := ttnpb.NewApplicationRegistryClient(cc)

		list, err := reg.List(ctx, &ttnpb.ListApplicationsRequest{
			FieldMask:    &pbtypes.FieldMask{Paths: []string{"name"}},
			Collaborator: userID.GetOrganizationOrUserIdentifiers(),
			Limit:        2,
			Page:         1,
		}, creds)

		a.So(err, should.BeNil)
		if a.So(list, should.NotBeNil) {
			a.So(list.Applications, should.HaveLength, 2)
		}

		list, err = reg.List(ctx, &ttnpb.ListApplicationsRequest{
			FieldMask:    &pbtypes.FieldMask{Paths: []string{"name"}},
			Collaborator: userID.OrganizationOrUserIdentifiers(),
			Limit:        2,
			Page:         2,
		}, creds)

		a.So(err, should.BeNil)
		if a.So(list, should.NotBeNil) {
			a.So(list.Applications, should.HaveLength, 1)
		}

		list, err = reg.List(ctx, &ttnpb.ListApplicationsRequest{
			FieldMask:    &pbtypes.FieldMask{Paths: []string{"name"}},
			Collaborator: userID.OrganizationOrUserIdentifiers(),
			Limit:        2,
			Page:         3,
		}, creds)

		a.So(err, should.BeNil)
		if a.So(list, should.NotBeNil) {
			a.So(list.Applications, should.BeEmpty)
		}
	})
}
