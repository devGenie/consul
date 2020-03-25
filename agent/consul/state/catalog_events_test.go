package state

import (
	"fmt"
	"testing"

	"github.com/hashicorp/consul/agent/agentpb"
	"github.com/hashicorp/consul/agent/structs"
	"github.com/hashicorp/consul/api"
	"github.com/hashicorp/consul/types"
	"github.com/stretchr/testify/require"
)

type regOption func(req *structs.RegisterRequest) error
type eventOption func(e *agentpb.Event) error

func testNodeRegistration(t *testing.T, opts ...regOption) *structs.RegisterRequest {
	r := &structs.RegisterRequest{
		Datacenter: "dc1",
		ID:         "11111111-2222-3333-4444-555555555555",
		Node:       "node1",
		Address:    "10.10.10.10",
		Checks: structs.HealthChecks{
			&structs.HealthCheck{
				CheckID: "serf-health",
				Name:    "serf-health",
				Node:    "node1",
				Status:  api.HealthPassing,
			},
		},
	}
	for _, opt := range opts {
		err := opt(r)
		require.NoError(t, err)
	}
	return r
}

func testServiceRegistration(t *testing.T, svc string, opts ...regOption) *structs.RegisterRequest {
	// note: don't pass opts or they might get applied twice!
	r := testNodeRegistration(t)
	r.Service = &structs.NodeService{
		ID:      svc,
		Service: svc,
		Port:    8080,
	}
	r.Checks = append(r.Checks,
		&structs.HealthCheck{
			CheckID:     types.CheckID("service:" + svc),
			Name:        "service:" + svc,
			Node:        "node1",
			ServiceID:   svc,
			ServiceName: svc,
			Type:        "ttl",
			Status:      api.HealthPassing,
		})
	for _, opt := range opts {
		err := opt(r)
		require.NoError(t, err)
	}
	return r
}

func testServiceHealthEvent(t *testing.T, svc string, opts ...eventOption) agentpb.Event {
	e := agentpb.Event{
		Topic: agentpb.Topic_ServiceHealth,
		Key:   svc,
		Index: 100,
		Payload: &agentpb.Event_ServiceHealth{
			ServiceHealth: &agentpb.ServiceHealthUpdate{
				Op: agentpb.CatalogOp_Register,
				CheckServiceNode: &agentpb.CheckServiceNode{
					Node: &agentpb.Node{
						ID:         "11111111-2222-3333-4444-555555555555",
						Node:       "node1",
						Address:    "10.10.10.10",
						Datacenter: "dc1",
						RaftIndex: agentpb.RaftIndex{
							CreateIndex: 100,
							ModifyIndex: 100,
						},
					},
					Service: &agentpb.NodeService{
						ID:      svc,
						Service: svc,
						Port:    8080,
						Weights: &agentpb.Weights{
							Passing: 1,
							Warning: 1,
						},
						// Empty sadness
						Proxy: agentpb.ConnectProxyConfig{
							MeshGateway: &agentpb.MeshGatewayConfig{},
							Expose:      &agentpb.ExposeConfig{},
						},
						EnterpriseMeta: &agentpb.EnterpriseMeta{},
						RaftIndex: agentpb.RaftIndex{
							CreateIndex: 100,
							ModifyIndex: 100,
						},
					},
					Checks: []*agentpb.HealthCheck{
						&agentpb.HealthCheck{
							Node:           "node1",
							CheckID:        "serf-health",
							Name:           "serf-health",
							Status:         "passing",
							EnterpriseMeta: &agentpb.EnterpriseMeta{},
							RaftIndex: agentpb.RaftIndex{
								CreateIndex: 100,
								ModifyIndex: 100,
							},
						},
						&agentpb.HealthCheck{
							Node:           "node1",
							CheckID:        types.CheckID("service:" + svc),
							Name:           "service:" + svc,
							ServiceID:      svc,
							ServiceName:    svc,
							Type:           "ttl",
							Status:         "passing",
							EnterpriseMeta: &agentpb.EnterpriseMeta{},
							RaftIndex: agentpb.RaftIndex{
								CreateIndex: 100,
								ModifyIndex: 100,
							},
						},
					},
				},
			},
		},
	}

	for _, opt := range opts {
		err := opt(&e)
		require.NoError(t, err)
	}
	return e
}

func testServiceHealthDeregistrationEvent(t *testing.T, svc string, opts ...eventOption) agentpb.Event {
	e := agentpb.Event{
		Topic: agentpb.Topic_ServiceHealth,
		Key:   svc,
		Index: 100,
		Payload: &agentpb.Event_ServiceHealth{
			ServiceHealth: &agentpb.ServiceHealthUpdate{
				Op: agentpb.CatalogOp_Deregister,
				CheckServiceNode: &agentpb.CheckServiceNode{
					Node: &agentpb.Node{
						Node: "node1",
					},
					Service: &agentpb.NodeService{
						ID:      svc,
						Service: svc,
						Port:    8080,
						Weights: &agentpb.Weights{
							Passing: 1,
							Warning: 1,
						},
						// Empty sadness
						Proxy: agentpb.ConnectProxyConfig{
							MeshGateway: &agentpb.MeshGatewayConfig{},
							Expose:      &agentpb.ExposeConfig{},
						},
						EnterpriseMeta: &agentpb.EnterpriseMeta{},
						RaftIndex: agentpb.RaftIndex{
							// The original insertion index since a delete doesn't update this.
							CreateIndex: 10,
							ModifyIndex: 10,
						},
					},
				},
			},
		},
	}
	for _, opt := range opts {
		err := opt(&e)
		require.NoError(t, err)
	}
	return e
}

// regConnectNative option converts the base registration into a Connect-native
// one.
func regConnectNative(req *structs.RegisterRequest) error {
	if req.Service == nil {
		return nil
	}
	req.Service.Connect.Native = true
	return nil
}

// regSidecar option converts the base registration request
// into the registration for it's sidecar service.
func regSidecar(req *structs.RegisterRequest) error {
	if req.Service == nil {
		return nil
	}
	svc := req.Service.Service

	req.Service.Kind = structs.ServiceKindConnectProxy
	req.Service.ID = svc + "_sidecar_proxy"
	req.Service.Service = svc + "_sidecar_proxy"
	req.Service.Port = 20000 + req.Service.Port

	req.Service.Proxy.DestinationServiceName = svc
	req.Service.Proxy.DestinationServiceID = svc

	// Convert the check to point to the right ID now. This isn't totally
	// realistic - sidecars should have alias checks etc but this is good enough
	// to test this code path.
	if len(req.Checks) >= 2 {
		req.Checks[1].CheckID = types.CheckID("service:" + svc + "_sidecar_proxy")
		req.Checks[1].ServiceID = svc + "_sidecar_proxy"
	}

	return nil
}

// regNodeCheckFail option converts the base registration request
// into a registration with the node-level health check failing
func regNodeCheckFail(req *structs.RegisterRequest) error {
	req.Checks[0].Status = api.HealthCritical
	return nil
}

// regServiceCheckFail option converts the base registration request
// into a registration with the service-level health check failing
func regServiceCheckFail(req *structs.RegisterRequest) error {
	req.Checks[1].Status = api.HealthCritical
	return nil
}

// regMutatePort option alters the base registration service port by a relative
// amount to simulate a service change. Can be used with regSidecar since it's a
// relative change (+10).
func regMutatePort(req *structs.RegisterRequest) error {
	if req.Service == nil {
		return nil
	}
	req.Service.Port += 10
	return nil
}

// regRenameService option alters the base registration service name but not
// it's ID simulating a service being renamed while it's ID is maintained
// separately e.g. by a scheduler. This is an edge case but an important one as
// it changes which topic key events propagate.
func regRenameService(req *structs.RegisterRequest) error {
	if req.Service == nil {
		return nil
	}
	isSidecar := req.Service.Kind == structs.ServiceKindConnectProxy

	if !isSidecar {
		req.Service.Service += "_changed"
		// Update service checks
		if len(req.Checks) >= 2 {
			req.Checks[1].ServiceName += "_changed"
		}
		return nil
	}
	// This is a sidecar, it's not really realistic but lets only update the
	// fields necessary to make it work again with the new service name to be sure
	// we get the right result. This is certainly possible if not likely so a
	// valid case.

	// We don't need to update out own details, only the name of the destination
	req.Service.Proxy.DestinationServiceName += "_changed"

	return nil
}

// regRenameNode option alters the base registration node name by adding the
// _changed suffix.
func regRenameNode(req *structs.RegisterRequest) error {
	req.Node += "_changed"
	for i := range req.Checks {
		req.Checks[i].Node = req.Node
	}
	return nil
}

// regNode2 option alters the base registration to be on a different node.
func regNode2(req *structs.RegisterRequest) error {
	req.Node = "node2"
	req.ID = "22222222-2222-3333-4444-555555555555"
	for i := range req.Checks {
		req.Checks[i].Node = req.Node
	}
	return nil
}

// regNodeMeta option alters the base registration node to add some meta data.
func regNodeMeta(req *structs.RegisterRequest) error {
	req.NodeMeta = map[string]string{"foo": "bar"}
	return nil
}

// evNodeUnchanged option converts the event to reset the node and node check
// raft indexes to the original value where we expect the node not to have been
// changed in the mutation.
func evNodeUnchanged(e *agentpb.Event) error {
	// If the node wasn't touched, its modified index and check's modified
	// indexes should be the original ones.
	csn := e.GetServiceHealth().CheckServiceNode

	// Check this isn't a dereg event with made up/placeholder node info
	if csn.Node.CreateIndex == 0 {
		return nil
	}
	csn.Node.CreateIndex = 10
	csn.Node.ModifyIndex = 10
	csn.Checks[0].CreateIndex = 10
	csn.Checks[0].ModifyIndex = 10
	return nil
}

// evServiceUnchanged option converts the event to reset the service and service
// check raft indexes to the original value where we expect the service record
// not to have been changed in the mutation.
func evServiceUnchanged(e *agentpb.Event) error {
	// If the node wasn't touched, its modified index and check's modified
	// indexes should be the original ones.
	csn := e.GetServiceHealth().CheckServiceNode

	csn.Service.CreateIndex = 10
	csn.Service.ModifyIndex = 10
	if len(csn.Checks) > 1 {
		csn.Checks[1].CreateIndex = 10
		csn.Checks[1].ModifyIndex = 10
	}
	return nil
}

// evConnectNative option converts the base event to represent a connect-native
// service instance.
func evConnectNative(e *agentpb.Event) error {
	e.GetServiceHealth().CheckServiceNode.Service.Connect.Native = true
	return nil
}

// evConnectTopic option converts the base event to the equivalent event that
// should be published to the connect topic. When needed it should be applied
// first as several other options (notable evSidecar) change behavior subtly
// depending on which topic they are published to and they determin this from
// the event.
func evConnectTopic(e *agentpb.Event) error {
	e.Topic = agentpb.Topic_ServiceHealthConnect
	return nil
}

// evSidecar option converts the base event to the health (not connect) event
// expected from the sidecar proxy registration for that service instead. When
// needed it should be applied after any option that changes topic (e.g.
// evConnectTopic) but before other options that might change behavior subtly
// depending on whether it's a sidecar or regular service event (e.g.
// evRenameService).
func evSidecar(e *agentpb.Event) error {
	csn := e.GetServiceHealth().CheckServiceNode

	svc := csn.Service.Service

	csn.Service.Kind = structs.ServiceKindConnectProxy
	csn.Service.ID = svc + "_sidecar_proxy"
	csn.Service.Service = svc + "_sidecar_proxy"
	csn.Service.Port = 20000 + csn.Service.Port

	csn.Service.Proxy.DestinationServiceName = svc
	csn.Service.Proxy.DestinationServiceID = svc

	// Convert the check to point to the right ID now. This isn't totally
	// realistic - sidecars should have alias checks etc but this is good enough
	// to test this code path.
	if len(csn.Checks) >= 2 {
		csn.Checks[1].CheckID = types.CheckID("service:" + svc + "_sidecar_proxy")
		csn.Checks[1].ServiceID = svc + "_sidecar_proxy"
		csn.Checks[1].ServiceName = svc + "_sidecar_proxy"
	}

	// Update event key to be the proxy service name, but only if this is not
	// already in the connect topic
	if e.Topic != agentpb.Topic_ServiceHealthConnect {
		e.Key = csn.Service.Service
	}
	return nil
}

// evMutatePort option alters the base event service port by a relative
// amount to simulate a service change. Can be used with evSidecar since it's a
// relative change (+10).
func evMutatePort(e *agentpb.Event) error {
	e.GetServiceHealth().CheckServiceNode.Service.Port += 10
	return nil
}

// evNodeMutated option alters the base event node to set it's CreateIndex
// (but not modify index) to the setup index. This expresses that we expect the
// node record originally created in setup to have been mutated during the
// update.
func evNodeMutated(e *agentpb.Event) error {
	e.GetServiceHealth().CheckServiceNode.Node.CreateIndex = 10
	return nil
}

// evServiceMutated option alters the base event service to set it's CreateIndex
// (but not modify index) to the setup index. This expresses that we expect the
// service record originally created in setup to have been mutated during the
// update.
func evServiceMutated(e *agentpb.Event) error {
	e.GetServiceHealth().CheckServiceNode.Service.CreateIndex = 10
	return nil
}

// evChecksMutated option alters the base event service check to set it's
// CreateIndex (but not modify index) to the setup index. This expresses that we
// expect the service check records originally created in setup to have been
// mutated during the update. NOTE: this must be sequenced after
// evServiceUnchanged if both are used.
func evChecksMutated(e *agentpb.Event) error {
	e.GetServiceHealth().CheckServiceNode.Checks[1].CreateIndex = 10
	e.GetServiceHealth().CheckServiceNode.Checks[1].ModifyIndex = 100
	return nil
}

// evNodeChecksMutated option alters the base event node check to set it's
// CreateIndex (but not modify index) to the setup index. This expresses that we
// expect the node check records originally created in setup to have been
// mutated during the update. NOTE: this must be sequenced after evNodeUnchanged
// if both are used.
func evNodeChecksMutated(e *agentpb.Event) error {
	e.GetServiceHealth().CheckServiceNode.Checks[0].CreateIndex = 10
	e.GetServiceHealth().CheckServiceNode.Checks[0].ModifyIndex = 100
	return nil
}

// evChecksUnchanged option alters the base event service to set all check raft
// indexes to the setup index. This expresses that we expect none of the checks
// to have changed in the update.
func evChecksUnchanged(e *agentpb.Event) error {
	csn := e.GetServiceHealth().CheckServiceNode
	for i := range csn.Checks {
		csn.Checks[i].CreateIndex = 10
		csn.Checks[i].ModifyIndex = 10
	}
	return nil
}

// evRenameService option alters the base event service to change the service
// name but not ID simulating an in-place service rename.
func evRenameService(e *agentpb.Event) error {
	csn := e.GetServiceHealth().CheckServiceNode
	isSidecar := csn.Service.Kind == structs.ServiceKindConnectProxy

	if !isSidecar {
		csn.Service.Service += "_changed"
		// Update service checks
		if len(csn.Checks) >= 2 {
			csn.Checks[1].ServiceName += "_changed"
		}
		e.Key += "_changed"
		return nil
	}
	// This is a sidecar, it's not really realistic but lets only update the
	// fields necessary to make it work again with the new service name to be sure
	// we get the right result. This is certainly possible if not likely so a
	// valid case.

	// We don't need to update out own details, only the name of the destination
	csn.Service.Proxy.DestinationServiceName += "_changed"

	// If this is the connect topic we need to change the key too
	if e.Topic == agentpb.Topic_ServiceHealthConnect {
		e.Key += "_changed"
	}
	return nil
}

// evNodeMeta option alters the base event node to add some meta data.
func evNodeMeta(e *agentpb.Event) error {
	csn := e.GetServiceHealth().CheckServiceNode
	csn.Node.Meta = map[string]string{"foo": "bar"}
	return nil
}

// evRenameNode option alters the base event node name.
func evRenameNode(e *agentpb.Event) error {
	csn := e.GetServiceHealth().CheckServiceNode
	csn.Node.Node += "_changed"
	for i := range csn.Checks {
		csn.Checks[i].Node = csn.Node.Node
	}
	return nil
}

// evNode2 option alters the base event to refer to a different node
func evNode2(e *agentpb.Event) error {
	csn := e.GetServiceHealth().CheckServiceNode
	csn.Node.Node = "node2"
	// Only change ID if it's set (e.g. it's not in a deregistration event)
	if csn.Node.ID != "" {
		csn.Node.ID = "22222222-2222-3333-4444-555555555555"
	}
	for i := range csn.Checks {
		csn.Checks[i].Node = csn.Node.Node
	}
	return nil
}

// evNodeCheckFail option alters the base event to set the node-level health
// check to be failing
func evNodeCheckFail(e *agentpb.Event) error {
	csn := e.GetServiceHealth().CheckServiceNode
	csn.Checks[0].Status = api.HealthCritical
	return nil
}

// evNodeCheckDelete option alters the base event to remove the node-level
// health check
func evNodeCheckDelete(e *agentpb.Event) error {
	csn := e.GetServiceHealth().CheckServiceNode
	// Ensure this is idempotent as we sometimes get called multiple times..
	if len(csn.Checks) > 0 && csn.Checks[0].ServiceID == "" {
		csn.Checks = csn.Checks[1:]
	}
	return nil
}

// evServiceCheckFail option alters the base event to set the service-level health
// check to be failing
func evServiceCheckFail(e *agentpb.Event) error {
	csn := e.GetServiceHealth().CheckServiceNode
	csn.Checks[1].Status = api.HealthCritical
	return nil
}

// evServiceCheckDelete option alters the base event to remove the service-level
// health check
func evServiceCheckDelete(e *agentpb.Event) error {
	csn := e.GetServiceHealth().CheckServiceNode
	// Ensure this is idempotent as we sometimes get called multiple times..
	if len(csn.Checks) > 1 && csn.Checks[1].ServiceID != "" {
		csn.Checks = csn.Checks[0:1]
	}
	return nil
}

func TestServiceHealthEventsFromChanges(t *testing.T) {
	cases := []struct {
		Name       string
		Setup      func(s *Store, tx *txnWrapper) error
		Mutate     func(s *Store, tx *txnWrapper) error
		WantEvents []agentpb.Event
		WantErr    bool
	}{
		{
			Name: "irrelevant events",
			Mutate: func(s *Store, tx *txnWrapper) error {
				return s.kvsSetTxn(tx, tx.Index, &structs.DirEntry{
					Key:   "foo",
					Value: []byte("bar"),
				}, false)
			},
			WantEvents: nil,
			WantErr:    false,
		},
		{
			Name: "service reg, new node",
			Mutate: func(s *Store, tx *txnWrapper) error {
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web"))
			},
			WantEvents: []agentpb.Event{
				testServiceHealthEvent(t, "web"),
			},
			WantErr: false,
		},
		{
			Name: "service reg, existing node",
			Setup: func(s *Store, tx *txnWrapper) error {
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db"))
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web"))
			},
			WantEvents: []agentpb.Event{
				// Should only publish new service
				testServiceHealthEvent(t, "web", evNodeUnchanged),
			},
			WantErr: false,
		},
		{
			Name: "service dereg, existing node",
			Setup: func(s *Store, tx *txnWrapper) error {
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				return nil
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				return s.deleteServiceTxn(tx, tx.Index, "node1", "web", nil)
			},
			WantEvents: []agentpb.Event{
				// Should only publish deregistration for that service
				testServiceHealthDeregistrationEvent(t, "web"),
			},
			WantErr: false,
		},
		{
			Name: "node dereg",
			Setup: func(s *Store, tx *txnWrapper) error {
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				return nil
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				return s.deleteNodeTxn(tx, tx.Index, "node1")
			},
			WantEvents: []agentpb.Event{
				// Should publish deregistration events for all services
				testServiceHealthDeregistrationEvent(t, "db"),
				testServiceHealthDeregistrationEvent(t, "web"),
			},
			WantErr: false,
		},
		{
			Name: "connect native reg, new node",
			Mutate: func(s *Store, tx *txnWrapper) error {
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regConnectNative))
			},
			WantEvents: []agentpb.Event{
				// We should see both a regular service health event as well as a connect
				// one.
				testServiceHealthEvent(t, "web", evConnectNative),
				testServiceHealthEvent(t, "web", evConnectNative, evConnectTopic),
			},
			WantErr: false,
		},
		{
			Name: "connect native reg, existing node",
			Setup: func(s *Store, tx *txnWrapper) error {
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db"))
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regConnectNative))
			},
			WantEvents: []agentpb.Event{
				// We should see both a regular service health event as well as a connect
				// one.
				testServiceHealthEvent(t, "web",
					evNodeUnchanged,
					evConnectNative),
				testServiceHealthEvent(t, "web",
					evNodeUnchanged,
					evConnectNative,
					evConnectTopic),
			},
			WantErr: false,
		},
		{
			Name: "connect native dereg, existing node",
			Setup: func(s *Store, tx *txnWrapper) error {
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}

				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regConnectNative))
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				return s.deleteServiceTxn(tx, tx.Index, "node1", "web", nil)
			},
			WantEvents: []agentpb.Event{
				// We should see both a regular service dereg event and a connect one
				testServiceHealthDeregistrationEvent(t, "web", evConnectNative),
				testServiceHealthDeregistrationEvent(t, "web", evConnectNative, evConnectTopic),
			},
			WantErr: false,
		},
		{
			Name: "connect sidecar reg, new node",
			Mutate: func(s *Store, tx *txnWrapper) error {
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar))
			},
			WantEvents: []agentpb.Event{
				// We should see both a regular service health event for the web service
				// another for the sidecar service and a connect event for web.
				testServiceHealthEvent(t, "web"),
				testServiceHealthEvent(t, "web", evSidecar),
				testServiceHealthEvent(t, "web", evConnectTopic, evSidecar),
			},
			WantErr: false,
		},
		{
			Name: "connect sidecar reg, existing node",
			Setup: func(s *Store, tx *txnWrapper) error {
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web"))
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar))
			},
			WantEvents: []agentpb.Event{
				// We should see both a regular service health event for the proxy
				// service and a connect one for the target service.
				testServiceHealthEvent(t, "web", evSidecar, evNodeUnchanged),
				testServiceHealthEvent(t, "web", evConnectTopic, evSidecar, evNodeUnchanged),
			},
			WantErr: false,
		},
		{
			Name: "connect sidecar dereg, existing node",
			Setup: func(s *Store, tx *txnWrapper) error {
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar))
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				// Delete only the sidecar
				return s.deleteServiceTxn(tx, tx.Index, "node1", "web_sidecar_proxy", nil)
			},
			WantEvents: []agentpb.Event{
				// We should see both a regular service dereg event and a connect one
				testServiceHealthDeregistrationEvent(t, "web", evSidecar),
				testServiceHealthDeregistrationEvent(t, "web", evConnectTopic, evSidecar),
			},
			WantErr: false,
		},
		{
			Name: "connect sidecar mutate svc",
			Setup: func(s *Store, tx *txnWrapper) error {
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar))
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				// Change port of the target service instance
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regMutatePort))
			},
			WantEvents: []agentpb.Event{
				// We should see the service topic update but not connect since proxy
				// details didn't change.
				testServiceHealthEvent(t, "web",
					evMutatePort,
					evNodeUnchanged,
					evServiceMutated,
					evChecksUnchanged,
				),
			},
			WantErr: false,
		},
		{
			Name: "connect sidecar mutate sidecar",
			Setup: func(s *Store, tx *txnWrapper) error {
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar))
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				// Change port of the sidecar service instance
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar, regMutatePort))
			},
			WantEvents: []agentpb.Event{
				// We should see the proxy service topic update and a connect update
				testServiceHealthEvent(t, "web",
					evSidecar,
					evMutatePort,
					evNodeUnchanged,
					evServiceMutated,
					evChecksUnchanged),
				testServiceHealthEvent(t, "web",
					evConnectTopic,
					evSidecar,
					evNodeUnchanged,
					evMutatePort,
					evServiceMutated,
					evChecksUnchanged),
			},
			WantErr: false,
		},
		{
			Name: "connect sidecar rename service",
			Setup: func(s *Store, tx *txnWrapper) error {
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar))
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				// Change service name but not ID, update proxy too
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regRenameService)); err != nil {
					return err
				}
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar, regRenameService))
			},
			WantEvents: []agentpb.Event{
				// We should see events to deregister the old service instance and the
				// old connect instance since we changed topic key for both. Then new
				// service and connect registrations. The proxy instance should also
				// change since it's not proxying a different service.
				testServiceHealthDeregistrationEvent(t, "web"),
				testServiceHealthEvent(t, "web",
					evRenameService,
					evServiceMutated,
					evNodeUnchanged,
					evChecksMutated,
				),
				testServiceHealthDeregistrationEvent(t, "web",
					evConnectTopic,
					evSidecar,
				),
				testServiceHealthEvent(t, "web",
					evSidecar,
					evRenameService,
					evNodeUnchanged,
					evServiceMutated,
					evChecksUnchanged,
				),
				testServiceHealthEvent(t, "web",
					evConnectTopic,
					evSidecar,
					evNodeUnchanged,
					evRenameService,
					evServiceMutated,
					evChecksUnchanged,
				),
			},
			WantErr: false,
		},
		{
			Name: "connect sidecar change destination service",
			Setup: func(s *Store, tx *txnWrapper) error {
				// Register a web_changed service
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web_changed")); err != nil {
					return err
				}
				// Also a web
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				// And a sidecar initially for web, will be moved to target web_changed
				// in Mutate.
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar))
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				// Change only the destination service of the proxy without a service
				// rename or deleting and recreating the proxy. This is far fetched but
				// still valid.
				return s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar, regRenameService))
			},
			WantEvents: []agentpb.Event{
				// We should only see service health events for the sidecar service
				// since the actual target services didn't change. But also should see
				// Connect topic dereg for the old name to update existing subscribers
				// for Connect/web.
				testServiceHealthDeregistrationEvent(t, "web",
					evConnectTopic,
					evSidecar,
				),
				testServiceHealthEvent(t, "web",
					evSidecar,
					evRenameService,
					evNodeUnchanged,
					evServiceMutated,
					evChecksUnchanged,
				),
				testServiceHealthEvent(t, "web",
					evConnectTopic,
					evSidecar,
					evNodeUnchanged,
					evRenameService,
					evServiceMutated,
					evChecksUnchanged,
				),
			},
			WantErr: false,
		},
		{
			Name: "multi-service node update",
			Setup: func(s *Store, tx *txnWrapper) error {
				// Register a db service
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				// Also a web
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				// With a connect sidecar
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar)); err != nil {
					return err
				}
				return nil
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				// Change only the node meta.
				return s.ensureRegistrationTxn(tx, tx.Index,
					testNodeRegistration(t, regNodeMeta))
			},
			WantEvents: []agentpb.Event{
				// We should see updates for all services and a connect update for the
				// sidecar's destination.
				testServiceHealthEvent(t, "db",
					evNodeMeta,
					evNodeMutated,
					evServiceUnchanged,
					evChecksUnchanged,
				),
				testServiceHealthEvent(t, "web",
					evNodeMeta,
					evNodeMutated,
					evServiceUnchanged,
					evChecksUnchanged,
				),
				testServiceHealthEvent(t, "web",
					evSidecar,
					evNodeMeta,
					evNodeMutated,
					evServiceUnchanged,
					evChecksUnchanged,
				),
				testServiceHealthEvent(t, "web",
					evConnectTopic,
					evSidecar,
					evNodeMeta,
					evNodeMutated,
					evServiceUnchanged,
					evChecksUnchanged,
				),
			},
			WantErr: false,
		},
		{
			Name: "multi-service node rename",
			Setup: func(s *Store, tx *txnWrapper) error {
				// Register a db service
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				// Also a web
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				// With a connect sidecar
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar)); err != nil {
					return err
				}
				return nil
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				// Change only the node NAME but not it's ID. We do it for every service
				// though since this is effectively what client agent anti-entropy would
				// do on a node rename. If we only rename the node it will have no
				// services registered afterwards.
				// Register a db service
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db", regRenameNode)); err != nil {
					return err
				}
				// Also a web
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regRenameNode)); err != nil {
					return err
				}
				// With a connect sidecar
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar, regRenameNode)); err != nil {
					return err
				}
				return nil
			},
			WantEvents: []agentpb.Event{
				// Node rename is implemented internally as a node delete and new node
				// insert after some renaming validation. So we should see full set of
				// new events for health, then the deletions of old services, then the
				// connect update and delete pair.
				testServiceHealthEvent(t, "db",
					evRenameNode,
					// Although we delete and re-insert, we do maintain the CreatedIndex
					// of the node record from the old one.
					evNodeMutated,
				),
				testServiceHealthEvent(t, "web",
					evRenameNode,
					evNodeMutated,
				),
				testServiceHealthEvent(t, "web",
					evSidecar,
					evRenameNode,
					evNodeMutated,
				),
				// dereg events for old node name services
				testServiceHealthDeregistrationEvent(t, "db"),
				testServiceHealthDeregistrationEvent(t, "web"),
				testServiceHealthDeregistrationEvent(t, "web", evSidecar),
				// Connect topic updates are last due to the way we add them
				testServiceHealthEvent(t, "web",
					evConnectTopic,
					evSidecar,
					evRenameNode,
					evNodeMutated,
				),
				testServiceHealthDeregistrationEvent(t, "web", evConnectTopic, evSidecar),
			},
			WantErr: false,
		},
		{
			Name: "multi-service node check failure",
			Setup: func(s *Store, tx *txnWrapper) error {
				// Register a db service
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				// Also a web
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				// With a connect sidecar
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar)); err != nil {
					return err
				}
				return nil
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				// Change only the node-level check status
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regNodeCheckFail)); err != nil {
					return err
				}
				return nil
			},
			WantEvents: []agentpb.Event{
				testServiceHealthEvent(t, "db",
					evNodeCheckFail,
					evNodeUnchanged,
					evServiceUnchanged,
					// Only the node check changed. This needs to come after evNodeUnchanged
					evNodeChecksMutated,
				),
				testServiceHealthEvent(t, "web",
					evNodeCheckFail,
					evNodeUnchanged,
					evServiceUnchanged,
					evNodeChecksMutated,
				),
				testServiceHealthEvent(t, "web",
					evSidecar,
					evNodeCheckFail,
					evNodeUnchanged,
					evServiceUnchanged,
					evNodeChecksMutated,
				),
				testServiceHealthEvent(t, "web",
					evConnectTopic,
					evSidecar,
					evNodeCheckFail,
					evNodeUnchanged,
					evServiceUnchanged,
					evNodeChecksMutated,
				),
			},
			WantErr: false,
		},
		{
			Name: "multi-service node service check failure",
			Setup: func(s *Store, tx *txnWrapper) error {
				// Register a db service
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				// Also a web
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				// With a connect sidecar
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar)); err != nil {
					return err
				}
				return nil
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				// Change the service-level check status
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regServiceCheckFail)); err != nil {
					return err
				}
				// Also change the service-level check status for the proxy. This is
				// analogous to what would happen with an alias check on the client side
				// - the proxies check would get updated at roughly the same time as the
				// target service check updates.
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar, regServiceCheckFail)); err != nil {
					return err
				}
				return nil
			},
			WantEvents: []agentpb.Event{
				// Should only see the events for that one service change, the sidecar
				// service and hence the connect topic for that service.
				testServiceHealthEvent(t, "web",
					evServiceCheckFail,
					evNodeUnchanged,
					evServiceUnchanged,
					evChecksMutated,
				),
				testServiceHealthEvent(t, "web",
					evSidecar,
					evServiceCheckFail,
					evNodeUnchanged,
					evServiceUnchanged,
					evChecksMutated,
				),
				testServiceHealthEvent(t, "web",
					evConnectTopic,
					evSidecar,
					evServiceCheckFail,
					evNodeUnchanged,
					evServiceUnchanged,
					evChecksMutated,
				),
			},
			WantErr: false,
		},
		{
			Name: "multi-service node node-level check delete",
			Setup: func(s *Store, tx *txnWrapper) error {
				// Register a db service
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				// Also a web
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				// With a connect sidecar
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar)); err != nil {
					return err
				}
				return nil
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				// Delete only the node-level check
				if err := s.deleteCheckTxn(tx, tx.Index, "node1", "serf-health", nil); err != nil {
					return err
				}
				return nil
			},
			WantEvents: []agentpb.Event{
				testServiceHealthEvent(t, "db",
					evNodeCheckDelete,
					evNodeUnchanged,
					evServiceUnchanged,
				),
				testServiceHealthEvent(t, "web",
					evNodeCheckDelete,
					evNodeUnchanged,
					evServiceUnchanged,
				),
				testServiceHealthEvent(t, "web",
					evSidecar,
					evNodeCheckDelete,
					evNodeUnchanged,
					evServiceUnchanged,
				),
				testServiceHealthEvent(t, "web",
					evConnectTopic,
					evSidecar,
					evNodeCheckDelete,
					evNodeUnchanged,
					evServiceUnchanged,
				),
			},
			WantErr: false,
		},
		{
			Name: "multi-service node service check delete",
			Setup: func(s *Store, tx *txnWrapper) error {
				// Register a db service
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}
				// Also a web
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				// With a connect sidecar
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar)); err != nil {
					return err
				}
				return nil
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				// Delete the service-level check for the main service
				if err := s.deleteCheckTxn(tx, tx.Index, "node1", "service:web", nil); err != nil {
					return err
				}
				// Also delete for a proxy
				if err := s.deleteCheckTxn(tx, tx.Index, "node1", "service:web_sidecar_proxy", nil); err != nil {
					return err
				}
				return nil
			},
			WantEvents: []agentpb.Event{
				// Should only see the events for that one service change, the sidecar
				// service and hence the connect topic for that service.
				testServiceHealthEvent(t, "web",
					evServiceCheckFail,
					evNodeUnchanged,
					evServiceUnchanged,
					evServiceCheckDelete,
				),
				testServiceHealthEvent(t, "web",
					evSidecar,
					evServiceCheckFail,
					evNodeUnchanged,
					evServiceUnchanged,
					evServiceCheckDelete,
				),
				testServiceHealthEvent(t, "web",
					evConnectTopic,
					evSidecar,
					evServiceCheckFail,
					evNodeUnchanged,
					evServiceUnchanged,
					evServiceCheckDelete,
				),
			},
			WantErr: false,
		},
		{
			Name: "many services on many nodes in one TX",
			Setup: func(s *Store, tx *txnWrapper) error {
				// Node1

				// Register a db service
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "db")); err != nil {
					return err
				}

				// Node2
				// Also a web
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regNode2)); err != nil {
					return err
				}
				// With a connect sidecar
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar, regNode2)); err != nil {
					return err
				}

				return nil
			},
			Mutate: func(s *Store, tx *txnWrapper) error {
				// In one transaction the operator moves the web service and it's
				// sidecar from node2 back to node1 and deletes them from node2

				if err := s.deleteServiceTxn(tx, tx.Index, "node2", "web", nil); err != nil {
					return err
				}
				if err := s.deleteServiceTxn(tx, tx.Index, "node2", "web_sidecar_proxy", nil); err != nil {
					return err
				}

				// Register those on node1
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web")); err != nil {
					return err
				}
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "web", regSidecar)); err != nil {
					return err
				}

				// And for good measure, add a new connect-native service to node2
				if err := s.ensureRegistrationTxn(tx, tx.Index,
					testServiceRegistration(t, "api", regConnectNative, regNode2)); err != nil {
					return err
				}

				return nil
			},
			WantEvents: []agentpb.Event{
				// We should see:
				//  - service dereg for web and proxy on node2
				//  - connect dereg for web on node2
				//  - service reg for web and proxy on node1
				//  - connect reg for web on node1
				//  - service reg for api on node2
				//  - connect reg for api on node2
				testServiceHealthDeregistrationEvent(t, "web", evNode2),
				testServiceHealthDeregistrationEvent(t, "web", evNode2, evSidecar),
				testServiceHealthDeregistrationEvent(t, "web",
					evConnectTopic,
					evNode2,
					evSidecar,
				),

				testServiceHealthEvent(t, "web", evNodeUnchanged),
				testServiceHealthEvent(t, "web", evSidecar, evNodeUnchanged),
				testServiceHealthEvent(t, "web", evConnectTopic, evSidecar, evNodeUnchanged),

				testServiceHealthEvent(t, "api",
					evNode2,
					evConnectNative,
					evNodeUnchanged,
				),
				testServiceHealthEvent(t, "api",
					evNode2,
					evConnectTopic,
					evConnectNative,
					evNodeUnchanged,
				),
			},
			WantErr: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			s := testStateStore(t)

			if tc.Setup != nil {
				// Bypass the publish mechanism for this test or we get into odd
				// recursive stuff...
				setupTx := s.db.WriteTxn(10)
				require.NoError(t, tc.Setup(s, setupTx))
				// Commit the underlying transaction without using wrapped Commit so we
				// avoid the whole event publishing system for setup here. It _should_
				// work but it makes debugging test hard as it will call the function
				// under test for the setup data...
				setupTx.Txn.Commit()
			}

			tx := s.db.WriteTxn(100)
			require.NoError(t, tc.Mutate(s, tx))

			// Note we call the func under test directly rather than publishChanges so
			// we can test this in isolation.
			got, err := s.ServiceHealthEventsFromChanges(tx, tx.Changes())
			if tc.WantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Make sure we have the right events, only taking ordering into account
			// where it matters to account for non-determinism.
			requireEventsInCorrectPartialOrder(t, tc.WantEvents, got, func(e agentpb.Event) string {
				// We need events affecting unique registrations to be ordered, within a topic
				csn := e.GetServiceHealth().CheckServiceNode
				return fmt.Sprintf("%s/%s/%s", e.Topic, csn.Node.Node, csn.Service.Service)
			})
		})
	}
}

// requireEventsInCorrectPartialOrder compares that the expected set of events
// was emitted. It allows for _independent_ events to be emitted in any order -
// this can be important because even though the transaction processing is all
// strictly ordered up until the processing func, grouping multiple updates that
// affect the same logical entity may be necessary and may impose random
// ordering changes on the eventual events if a map is used. We only care that
// events _affecting the same topic and key_ are ordered correctly with respect
// to the "expected" set of events so this helper asserts that.
//
// The caller provides a func that can return a partition key for the given
// event types and we assert that all events with the same partition key are
// deliveries in the same order. Note that this is not necessarily the same as
// topic/key since for example in Catalog only events about a specific service
// _instance_ need to be ordered while topic and key are more general.
func requireEventsInCorrectPartialOrder(t *testing.T, want, got []agentpb.Event,
	partKey func(agentpb.Event) string) {
	t.Helper()

	// Partion both arrays by topic/key
	wantParts := make(map[string][]agentpb.Event)
	gotParts := make(map[string][]agentpb.Event)

	for _, e := range want {
		k := partKey(e)
		wantParts[k] = append(wantParts[k], e)
	}
	for _, e := range got {
		k := partKey(e)
		gotParts[k] = append(gotParts[k], e)
	}

	//q.Q(wantParts, gotParts)

	for k, want := range wantParts {
		require.Equal(t, want, gotParts[k], "got incorrect events for partition: %s", k)
	}

	for k, got := range gotParts {
		if _, ok := wantParts[k]; !ok {
			require.Equal(t, nil, got, "got unwanted events for partition: %s", k)
		}
	}
}