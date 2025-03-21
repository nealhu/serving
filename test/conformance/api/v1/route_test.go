// +build e2e

/*
Copyright 2019 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"context"
	"net/url"
	"testing"

	pkgtest "knative.dev/pkg/test"
	"knative.dev/pkg/test/spoof"
	v1 "knative.dev/serving/pkg/apis/serving/v1"
	rtesting "knative.dev/serving/pkg/testing/v1"
	"knative.dev/serving/test"
	v1test "knative.dev/serving/test/v1"
)

func assertResourcesUpdatedWhenRevisionIsReady(t *testing.T, clients *test.Clients, names test.ResourceNames, url *url.URL, expectedGeneration, expectedText string) {
	t.Log("When the Route reports as Ready, everything should be ready.")
	if err := v1test.WaitForRouteState(clients.ServingClient, names.Route, v1test.IsRouteReady, "RouteIsReady"); err != nil {
		t.Fatalf("The Route %s was not marked as Ready to serve traffic to Revision %s: %v", names.Route, names.Revision, err)
	}

	t.Log("Serves the expected data at the endpoint")

	_, err := pkgtest.CheckEndpointState(
		context.Background(),
		clients.KubeClient,
		t.Logf,
		url,
		spoof.MatchesAllOf(spoof.IsStatusOK, spoof.MatchesBody(expectedText)),
		"CheckEndpointToServeText",
		test.ServingFlags.ResolvableDomain,
		test.AddRootCAtoTransport(context.Background(), t.Logf, clients, test.ServingFlags.HTTPS))
	if err != nil {
		t.Fatalf("The endpoint for Route %s at %s didn't serve the expected text %q: %v", names.Route, url, expectedText, err)
	}

	// We want to verify that the endpoint works as soon as Ready: True, but there are a bunch of other pieces of state that we validate for conformance.
	t.Log("The Revision will be marked as Ready when it can serve traffic")
	err = v1test.CheckRevisionState(clients.ServingClient, names.Revision, v1test.IsRevisionReady)
	if err != nil {
		t.Fatalf("Revision %s did not become ready to serve traffic: %v", names.Revision, err)
	}
	t.Log("The Revision will be annotated with the generation")
	err = v1test.CheckRevisionState(clients.ServingClient, names.Revision, v1test.IsRevisionAtExpectedGeneration(expectedGeneration))
	if err != nil {
		t.Fatalf("Revision %s did not have an expected annotation with generation %s: %v", names.Revision, expectedGeneration, err)
	}
	t.Log("Updates the Configuration that the Revision is ready")
	err = v1test.CheckConfigurationState(clients.ServingClient, names.Config, func(c *v1.Configuration) (bool, error) {
		return c.Status.LatestReadyRevisionName == names.Revision, nil
	})
	if err != nil {
		t.Fatalf("The Configuration %s was not updated indicating that the Revision %s was ready: %v", names.Config, names.Revision, err)
	}
	t.Log("Updates the Route to route traffic to the Revision")
	if err := v1test.CheckRouteState(clients.ServingClient, names.Route, v1test.AllRouteTrafficAtRevision(names)); err != nil {
		t.Fatalf("The Route %s was not updated to route traffic to the Revision %s: %v", names.Route, names.Revision, err)
	}
}

func getRouteURL(clients *test.Clients, names test.ResourceNames) (*url.URL, error) {
	var url *url.URL

	err := v1test.CheckRouteState(
		clients.ServingClient,
		names.Route,
		func(r *v1.Route) (bool, error) {
			if r.Status.URL == nil {
				return false, nil
			}
			url = r.Status.URL.URL()
			return url != nil, nil
		},
	)

	return url, err
}

// TestRouteGetAndList tests Route GET and LIST using Service as the only resource that we create, as Route CREATE is not required in the Spec.
func TestRouteGetAndList(t *testing.T) {
	t.Parallel()
	clients := test.Setup(t)

	names := test.ResourceNames{
		Service: test.ObjectNameForTest(t),
		Image:   test.PizzaPlanet1,
	}

	// Clean up on test failure or interrupt
	test.EnsureTearDown(t, clients, &names)

	// Setup initial Service
	if _, err := v1test.CreateServiceReady(t, clients, &names); err != nil {
		t.Fatalf("Failed to create initial Service %v: %v", names.Service, err)
	}

	route, err := v1test.GetRoute(clients, names.Route)
	if err != nil {
		t.Fatal("Getting route failed")
	}

	routes, err := v1test.GetRoutes(clients)
	if err != nil {
		t.Fatal("Getting routes failed")
	}
	var routeFound = false
	for _, routeItem := range routes.Items {
		t.Logf("Route Returned: %s", routeItem.Name)
		if routeItem.Name == route.Name {
			routeFound = true
		}
	}

	if !routeFound {
		t.Fatal("The Route that was previously created was not found by listing all Routes.")
	}
}

func TestRouteCreation(t *testing.T) {
	if test.ServingFlags.DisableOptionalAPI {
		t.Skip("Route create/patch/replace APIs are not required by Knative Serving API Specification")
	}

	t.Parallel()
	clients := test.Setup(t)

	var objects v1test.ResourceObjects
	svcName := test.ObjectNameForTest(t)
	names := test.ResourceNames{
		Config:        svcName,
		Route:         svcName,
		TrafficTarget: svcName,
		Image:         test.PizzaPlanet1,
	}

	test.EnsureTearDown(t, clients, &names)

	t.Log("Creating a new Route and Configuration")
	config, err := v1test.CreateConfiguration(t, clients, names)
	if err != nil {
		t.Fatal("Failed to create Configuration:", err)
	}
	objects.Config = config

	route, err := v1test.CreateRoute(t, clients, names)
	if err != nil {
		t.Fatal("Failed to create Route:", err)
	}
	objects.Route = route

	t.Log("The Configuration will be updated with the name of the Revision")
	names.Revision, err = v1test.WaitForConfigLatestPinnedRevision(clients, names)
	if err != nil {
		t.Fatalf("Configuration %s was not updated with the new revision: %v", names.Config, err)
	}

	url, err := getRouteURL(clients, names)
	if err != nil {
		t.Fatalf("Failed to get URL from route %s: %v", names.Route, err)
	}

	t.Log("The Route URL is:", url)
	assertResourcesUpdatedWhenRevisionIsReady(t, clients, names, url, "1", test.PizzaPlanetText1)

	// We start a prober at background thread to test if Route is always healthy even during Route update.
	prober := test.RunRouteProber(t.Logf, clients, url, test.AddRootCAtoTransport(context.Background(), t.Logf, clients, test.ServingFlags.HTTPS))
	defer test.AssertProberDefault(t, prober)

	t.Log("Updating the Configuration to use a different image")
	objects.Config, err = v1test.PatchConfig(t, clients, objects.Config, withConfigImage(pkgtest.ImagePath(test.PizzaPlanet2)))
	if err != nil {
		t.Fatalf("Patch update for Configuration %s with new image %s failed: %v", names.Config, test.PizzaPlanet2, err)
	}

	t.Log("Since the Configuration was updated a new Revision will be created and the Configuration will be updated")
	names.Revision, err = v1test.WaitForConfigLatestPinnedRevision(clients, names)
	if err != nil {
		t.Fatalf("Configuration %s was not updated with the Revision for image %s: %v", names.Config, test.PizzaPlanet2, err)
	}

	assertResourcesUpdatedWhenRevisionIsReady(t, clients, names, url, "2", test.PizzaPlanetText2)
}

// withConfigImage sets the container image to be the provided string.
func withConfigImage(img string) rtesting.ConfigOption {
	return func(cfg *v1.Configuration) {
		cfg.Spec.Template.Spec.Containers[0].Image = img
	}
}
