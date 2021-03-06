package terraform

import (
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/zclconf/go-cty/cty"

	"github.com/hashicorp/terraform/addrs"
	"github.com/hashicorp/terraform/configs/configschema"
	"github.com/hashicorp/terraform/providers"
	"github.com/hashicorp/terraform/states"
)

func TestEvalUpdateStateHook(t *testing.T) {
	mockHook := new(MockHook)

	state := states.NewState()
	state.Module(addrs.RootModuleInstance).SetLocalValue("foo", cty.StringVal("hello"))

	ctx := new(MockEvalContext)
	ctx.HookHook = mockHook
	ctx.StateState = state.SyncWrapper()

	node := &EvalUpdateStateHook{}
	if _, err := node.Eval(ctx); err != nil {
		t.Fatalf("err: %s", err)
	}

	if !mockHook.PostStateUpdateCalled {
		t.Fatal("should call PostStateUpdate")
	}
	if mockHook.PostStateUpdateState.LocalValue(addrs.LocalValue{Name: "foo"}.Absolute(addrs.RootModuleInstance)) != cty.StringVal("hello") {
		t.Fatalf("wrong state passed to hook: %s", spew.Sdump(mockHook.PostStateUpdateState))
	}
}

func TestEvalReadState(t *testing.T) {
	var output *states.ResourceInstanceObject
	mockProvider := mockProviderWithResourceTypeSchema("aws_instance", &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {
				Type:     cty.String,
				Optional: true,
			},
		},
	})
	providerSchema := mockProvider.GetSchemaReturn
	provider := providers.Interface(mockProvider)

	cases := map[string]struct {
		Resources          map[string]*ResourceState
		Node               EvalNode
		ExpectedInstanceId string
	}{
		"ReadState gets primary instance state": {
			Resources: map[string]*ResourceState{
				"aws_instance.bar": &ResourceState{
					Primary: &InstanceState{
						ID: "i-abc123",
					},
				},
			},
			Node: &EvalReadState{
				Addr: addrs.Resource{
					Mode: addrs.ManagedResourceMode,
					Type: "aws_instance",
					Name: "bar",
				}.Instance(addrs.NoKey),
				Provider:       &provider,
				ProviderSchema: &providerSchema,

				Output: &output,
			},
			ExpectedInstanceId: "i-abc123",
		},
		"ReadStateDeposed gets deposed instance": {
			Resources: map[string]*ResourceState{
				"aws_instance.bar": &ResourceState{
					Deposed: []*InstanceState{
						&InstanceState{ID: "i-abc123"},
					},
				},
			},
			Node: &EvalReadStateDeposed{
				Addr: addrs.Resource{
					Mode: addrs.ManagedResourceMode,
					Type: "aws_instance",
					Name: "bar",
				}.Instance(addrs.NoKey),
				Key:            states.DeposedKey("00000001"), // shim from legacy state assigns 0th deposed index this key
				Provider:       &provider,
				ProviderSchema: &providerSchema,

				Output: &output,
			},
			ExpectedInstanceId: "i-abc123",
		},
	}

	for k, c := range cases {
		t.Run(k, func(t *testing.T) {
			ctx := new(MockEvalContext)
			state := MustShimLegacyState(&State{
				Modules: []*ModuleState{
					&ModuleState{
						Path:      rootModulePath,
						Resources: c.Resources,
					},
				},
			})
			ctx.StateState = state.SyncWrapper()
			ctx.PathPath = addrs.RootModuleInstance

			result, err := c.Node.Eval(ctx)
			if err != nil {
				t.Fatalf("[%s] Got err: %#v", k, err)
			}

			expected := c.ExpectedInstanceId
			if !(result != nil && instanceObjectIdForTests(result.(*states.ResourceInstanceObject)) == expected) {
				t.Fatalf("[%s] Expected return with ID %#v, got: %#v", k, expected, result)
			}

			if !(output != nil && output.Value.GetAttr("id") == cty.StringVal(expected)) {
				t.Fatalf("[%s] Expected output with ID %#v, got: %#v", k, expected, output)
			}

			output = nil
		})
	}
}

func TestEvalWriteState(t *testing.T) {
	state := states.NewState()
	ctx := new(MockEvalContext)
	ctx.StateState = state.SyncWrapper()
	ctx.PathPath = addrs.RootModuleInstance

	mockProvider := mockProviderWithResourceTypeSchema("aws_instance", &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {
				Type:     cty.String,
				Optional: true,
			},
		},
	})
	providerSchema := mockProvider.GetSchemaReturn

	obj := &states.ResourceInstanceObject{
		Value: cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("i-abc123"),
		}),
		Status: states.ObjectReady,
	}
	node := &EvalWriteState{
		Addr: addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "aws_instance",
			Name: "foo",
		}.Instance(addrs.NoKey),

		State: &obj,

		ProviderSchema: &providerSchema,
		ProviderAddr:   addrs.RootModuleInstance.ProviderConfigDefault(addrs.NewDefaultProvider("aws")),
	}
	_, err := node.Eval(ctx)
	if err != nil {
		t.Fatalf("Got err: %#v", err)
	}

	checkStateString(t, state, `
aws_instance.foo:
  ID = i-abc123
  provider = provider["registry.terraform.io/hashicorp/aws"]
	`)
}

func TestEvalWriteStateDeposed(t *testing.T) {
	state := states.NewState()
	ctx := new(MockEvalContext)
	ctx.StateState = state.SyncWrapper()
	ctx.PathPath = addrs.RootModuleInstance

	mockProvider := mockProviderWithResourceTypeSchema("aws_instance", &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {
				Type:     cty.String,
				Optional: true,
			},
		},
	})
	providerSchema := mockProvider.GetSchemaReturn

	obj := &states.ResourceInstanceObject{
		Value: cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("i-abc123"),
		}),
		Status: states.ObjectReady,
	}
	node := &EvalWriteStateDeposed{
		Addr: addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "aws_instance",
			Name: "foo",
		}.Instance(addrs.NoKey),
		Key: states.DeposedKey("deadbeef"),

		State: &obj,

		ProviderSchema: &providerSchema,
		ProviderAddr:   addrs.RootModuleInstance.ProviderConfigDefault(addrs.NewDefaultProvider("aws")),
	}
	_, err := node.Eval(ctx)
	if err != nil {
		t.Fatalf("Got err: %#v", err)
	}

	checkStateString(t, state, `
aws_instance.foo: (1 deposed)
  ID = <not created>
  provider = provider["registry.terraform.io/hashicorp/aws"]
  Deposed ID 1 = i-abc123
	`)
}

// Same test as TestEvalUpdateStateHook, similar function, slightly different
// signature. The EvalUpdateStateHook test and function will be removed when the
// EvalNode Removal is complete.
func TestUpdateStateHook(t *testing.T) {
	mockHook := new(MockHook)

	state := states.NewState()
	state.Module(addrs.RootModuleInstance).SetLocalValue("foo", cty.StringVal("hello"))

	ctx := new(MockEvalContext)
	ctx.HookHook = mockHook
	ctx.StateState = state.SyncWrapper()

	if err := UpdateStateHook(ctx); err != nil {
		t.Fatalf("err: %s", err)
	}

	if !mockHook.PostStateUpdateCalled {
		t.Fatal("should call PostStateUpdate")
	}
	if mockHook.PostStateUpdateState.LocalValue(addrs.LocalValue{Name: "foo"}.Absolute(addrs.RootModuleInstance)) != cty.StringVal("hello") {
		t.Fatalf("wrong state passed to hook: %s", spew.Sdump(mockHook.PostStateUpdateState))
	}
}
