/*
Copyright The Kubernetes Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/checksum"
	cperrors "k8s.io/kubernetes/pkg/kubelet/checkpointmanager/errors"
)

func checkpointTestState() *CheckpointState {
	status := resourceapi.ResourceClaimStatus{
		Allocation: &resourceapi.AllocationResult{Devices: resourceapi.DeviceAllocationResult{
			Results: []resourceapi.DeviceRequestAllocationResult{{Request: "request", Driver: DriverName, Pool: "node", Device: "device",
				AdminAccess: new(false), ShareID: new(types.UID("share")),
				Tolerations:       []resourceapi.DeviceToleration{{Key: "test", Operator: resourceapi.DeviceTolerationOpEqual, Value: "yes", Effect: resourceapi.DeviceTaintEffectNoExecute, TolerationSeconds: new(int64(30))}},
				BindingConditions: []string{"Ready"}, BindingFailureConditions: []string{"Failed"},
				ConsumedCapacity:   map[resourceapi.QualifiedName]resource.Quantity{"memory": resource.MustParse("1Gi")},
				SkipNodeOperations: []resourceapi.SkipNodeOperation{resourceapi.SkipNodeOperation("Prepare")},
			}},
			Config: []resourceapi.DeviceAllocationConfiguration{{Source: resourceapi.AllocationConfigSourceClaim,
				DeviceConfiguration: resourceapi.DeviceConfiguration{Opaque: &resourceapi.OpaqueDeviceConfiguration{
					Driver: DriverName, Parameters: runtime.RawExtension{Raw: []byte(`{"apiVersion":"resource.nvidia.com/v1beta1","kind":"test","future":true}`)},
				}},
			}},
		}},
		ReservedFor: []resourceapi.ResourceClaimConsumerReference{{APIGroup: "", Resource: "pods", Name: "pod", UID: "pod-uid"}},
		Devices: []resourceapi.AllocatedDeviceStatus{{Driver: DriverName, Pool: "node", Device: "device", ShareID: new("share"),
			Conditions:  []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1, LastTransitionTime: metav1.Unix(123, 0), Reason: "Prepared", Message: "ready"}},
			Data:        &runtime.RawExtension{Raw: []byte(`{"health":"ok"}`)},
			NetworkData: &resourceapi.NetworkDeviceData{InterfaceName: "eth0", IPs: []string{"10.0.0.1/24"}, HardwareAddress: "00:00:00:00:00:01"},
		}},
	}
	device := &CheckpointedDevice{
		Requests: []string{"request"}, PoolName: "node", DeviceName: "device", CDIDeviceIDs: []string{"nvidia.com/test=device"},
		ShareID: new(types.UID("share")), Metadata: &kubeletplugin.DeviceMetadata{
			Attributes:  map[string]resourceapi.DeviceAttribute{"model": {StringValue: new("test")}},
			NetworkData: &resourceapi.NetworkDeviceData{InterfaceName: "eth0"},
		},
	}
	status.Allocation.NodeSelector = &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
		MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "node", Operator: corev1.NodeSelectorOpIn, Values: []string{"node"}}},
		MatchFields:      []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{"node"}}},
	}}}
	status.Allocation.AllocationTimestamp = new(metav1.Unix(123, 0))
	device.Metadata.Attributes["all"] = resourceapi.DeviceAttribute{IntValue: new(int64(1)), BoolValue: new(true), StringValue: new("test"), VersionValue: new("1.0.0"), IntValues: []int64{1}, BoolValues: []bool{true}, StringValues: []string{"test"}, VersionValues: []string{"1.0.0"}}
	state := &CheckpointState{NodeBootID: "boot", PreparedClaims: PreparedClaimsByUID{
		"completed": {CheckpointState: ClaimCheckpointStatePrepareCompleted, Name: "claim", Namespace: "ns", Status: status,
			PreparedDevices: PreparedDevices{{Devices: PreparedDeviceList{{Gpu: &PreparedGpu{Info: &GpuInfo{UUID: "gpu"}, Device: device}},
				{Mig: &PreparedMigDevice{Concrete: &MigLiveTuple{ParentMinor: 0, GIID: 1, CIID: 2, MigUUID: "mig", ParentUUID: "gpu", ParentPCIBusID: "0000:01:00.0"}, Device: device}},
				{Vfio: &PreparedVfioDevice{Info: &VfioDeviceInfo{UUID: "gpu-vfio", PciBusID: "0000:02:00.0"}, Device: device}}}, ConfigState: DeviceConfigState{MpsControlDaemonID: "mps", MpsApplied: new(true), TimeSliceApplied: new(false)}}}},
		"started": {CheckpointState: ClaimCheckpointStatePrepareStarted, Name: "pending", Namespace: "ns", Status: status},
	}}

	return state
}

func readTestCheckpoint(t *testing.T, data []byte) *Checkpoint {
	t.Helper()
	var cp Checkpoint
	require.NoError(t, cp.UnmarshalCheckpoint(data))
	require.NoError(t, cp.VerifyChecksum())
	return &cp
}

func TestCheckpointV3RoundTrip(t *testing.T) {
	state := checkpointTestState()
	original := &Checkpoint{State: state}
	data, err := original.MarshalCheckpoint()
	require.NoError(t, err)
	cp := readTestCheckpoint(t, data)
	require.NotNil(t, cp.V3)
	assert.Equal(t, state, cp.State)
	assert.Equal(t, "boot", cp.GetNodeBootID())
	latest, err := cp.ToLatestVersion()
	require.NoError(t, err)
	assert.Equal(t, state, latest.State)
	assert.Nil(t, original.V3, "writing must not mutate the input envelope")

	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &envelope))
	require.Contains(t, envelope, "v1")
	require.Contains(t, envelope, "v2")
	require.Contains(t, envelope, "v3")
	// Legacy fields have no bearing on V3 recovery, even if badly typed.
	envelope["v1"] = json.RawMessage(`"invalid legacy v1"`)
	envelope["v2"] = json.RawMessage(`{"checksum":"invalid"}`)
	envelope["checksum"] = json.RawMessage(`null`)
	altered, err := json.MarshalIndent(envelope, "", "  ")
	require.NoError(t, err)
	assert.Equal(t, state, readTestCheckpoint(t, altered).State)
}

func TestCheckpointV3InvalidDoesNotFallBack(t *testing.T) {
	data, err := (&Checkpoint{State: checkpointTestState()}).MarshalCheckpoint()
	require.NoError(t, err)
	tests := map[string]json.RawMessage{
		"null":                json.RawMessage(`null`),
		"wrong type":          json.RawMessage(`[]`),
		"missing data":        json.RawMessage(`{"checksum":0}`),
		"missing checksum":    json.RawMessage(`{"data":"{}"}`),
		"null data":           json.RawMessage(`{"data":null,"checksum":0}`),
		"null checksum":       json.RawMessage(`{"data":"{}","checksum":null}`),
		"wrong data type":     json.RawMessage(`{"data":{},"checksum":0}`),
		"wrong checksum type": json.RawMessage(`{"data":"{}","checksum":"0"}`),
		"negative checksum":   json.RawMessage(`{"data":"{}","checksum":-1}`),
		"fractional checksum": json.RawMessage(`{"data":"{}","checksum":0.5}`),
		"checksum overflow":   json.RawMessage(`{"data":"{}","checksum":18446744073709551616}`),
		"corrupt payload":     json.RawMessage(`{"data":"{}","checksum":0}`),
	}
	for name, payload := range map[string]string{
		"invalid JSON":       `{`,
		"null payload":       `null`,
		"array payload":      `[]`,
		"wrong boot ID type": `{"nodeBootID":1}`,
		"unknown state":      `{"preparedClaims":{"uid":{"checkpointState":"FutureState"}}}`,
		"unset state":        `{"preparedClaims":{"uid":{}}}`,
		"empty UID":          `{"preparedClaims":{"":{"checkpointState":"PrepareCompleted"}}}`,
		"null claim":         `{"preparedClaims":{"uid":null}}`,
		"null group":         `{"preparedClaims":{"uid":{"checkpointState":"PrepareCompleted","preparedDevices":[null]}}}`,
		"invalid device":     `{"preparedClaims":{"uid":{"checkpointState":"PrepareCompleted","preparedDevices":[{"devices":[{}]}]}}}`,
	} {
		v3 := CheckpointV3{Data: payload, Checksum: checksum.New([]byte(payload))}
		raw, err := json.Marshal(v3)
		require.NoError(t, err)
		tests[name] = raw
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			var envelope map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &envelope))
			envelope["v3"] = raw
			invalid, err := json.Marshal(envelope)
			require.NoError(t, err)
			var cp Checkpoint
			require.Error(t, cp.UnmarshalCheckpoint(invalid))
			assert.Nil(t, cp.State)
		})
	}
}

func TestCheckpointV3SavedBytes(t *testing.T) {
	payload := " { \"nodeBootID\": \"boot\", \"futureOptionalField\": true } \n"
	v3 := CheckpointV3{Data: payload, Checksum: checksum.New([]byte(payload))}
	data, err := json.Marshal(map[string]any{"v3": v3})
	require.NoError(t, err)
	cp := readTestCheckpoint(t, data)
	assert.Equal(t, payload, cp.V3.Data)
	assert.Equal(t, "boot", cp.GetNodeBootID())
	// Typed decoding discarded an unknown field; verification still uses saved bytes.
	require.NoError(t, cp.VerifyChecksum())
	cp.V3.Data = strings.ReplaceAll(cp.V3.Data, " ", "")
	require.Error(t, cp.VerifyChecksum())
}

func TestCheckpointLegacyUpgradeRollback(t *testing.T) {
	state := checkpointTestState()
	data, err := (&Checkpoint{State: state}).MarshalCheckpoint()
	require.NoError(t, err)
	var legacy Checkpoint
	require.NoError(t, json.Unmarshal(data, &legacy))
	// Simulate readers that ignore V3, including the original V1 checksum scope.
	require.NoError(t, legacy.VerifyChecksumV1())
	require.NoError(t, legacy.VerifyChecksumV2())
	assert.Len(t, legacy.V1.PreparedClaims, 1, "V1 excludes incomplete preparations")
	assert.Len(t, legacy.V2.PreparedClaims, len(state.PreparedClaims))
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			old := &Checkpoint{V1: legacy.V1, Checksum: legacy.Checksum}
			if version == "v2" {
				old.V2 = legacy.V2
			}
			oldData, err := json.Marshal(old)
			require.NoError(t, err)
			upgraded := readTestCheckpoint(t, oldData)
			upgraded, err = upgraded.ToLatestVersion()
			require.NoError(t, err)
			if version == "v2" {
				assert.Equal(t, state, upgraded.State)
			} else {
				require.Len(t, upgraded.State.PreparedClaims, 1)
				claim := upgraded.State.PreparedClaims["completed"]
				assert.Equal(t, ClaimCheckpointStatePrepareCompleted, claim.CheckpointState)
				assert.Equal(t, state.PreparedClaims["completed"].PreparedDevices, claim.PreparedDevices)
				assert.Equal(t, state.PreparedClaims["completed"].Status, claim.Status)
			}
			rewritten, err := upgraded.MarshalCheckpoint()
			require.NoError(t, err)
			assert.Equal(t, upgraded.State, readTestCheckpoint(t, rewritten).State)
		})
	}
	// Legacy corruption remains a hard error when V3 is absent.
	legacy.V3 = nil
	legacy.V2.NodeBootID = "tampered"
	bad, err := json.Marshal(legacy)
	require.NoError(t, err)
	var cp Checkpoint
	require.NoError(t, cp.UnmarshalCheckpoint(bad))
	require.Error(t, cp.VerifyChecksum())
}

func TestCheckpointEmptyAndUnsupported(t *testing.T) {
	latest, err := (&Checkpoint{}).ToLatestVersion()
	require.NoError(t, err)
	require.NotNil(t, latest.State.PreparedClaims)
	data, err := latest.MarshalCheckpoint()
	require.NoError(t, err)
	recovered, err := readTestCheckpoint(t, data).ToLatestVersion()
	require.NoError(t, err)
	assert.Empty(t, recovered.State.PreparedClaims)
	for _, raw := range []string{`null`, `{}`, `[]`, `{"v4":{}}`, `{"v1":null,"v2":null}`, `{`} {
		var cp Checkpoint
		require.Error(t, cp.UnmarshalCheckpoint([]byte(raw)), raw)
	}
}

func TestCheckpointManagerPreservesPreviousFileOnSerializationError(t *testing.T) {
	dir := t.TempDir()
	manager, err := checkpointmanager.NewCheckpointManager(dir)
	require.NoError(t, err)
	cp := &Checkpoint{State: checkpointTestState()}
	require.NoError(t, manager.CreateCheckpoint("checkpoint", cp))
	before, err := os.ReadFile(filepath.Join(dir, "checkpoint"))
	require.NoError(t, err)
	claim := cp.State.PreparedClaims["completed"]
	claim.Status.Allocation.Devices.Config[0].Opaque.Parameters.Raw = []byte(`{`)
	require.Error(t, manager.CreateCheckpoint("checkpoint", cp))
	after, err := os.ReadFile(filepath.Join(dir, "checkpoint"))
	require.NoError(t, err)
	assert.Equal(t, before, after)
	recovered := &Checkpoint{}
	require.NoError(t, manager.GetCheckpoint("checkpoint", recovered))
	assert.Equal(t, "boot", recovered.GetNodeBootID())
}

// These fixed fixtures were serialized using the original V1/V2 structs and
// checksum algorithms. Keep them unchanged when evolving working-state types.
func TestCheckpointLegacyFixtures(t *testing.T) {
	for _, version := range []string{"v1", "v2", "v2-optional-absent"} {
		t.Run(version, func(t *testing.T) {
			original, err := os.ReadFile(filepath.Join("testdata", "checkpoint-"+version+".json"))
			require.NoError(t, err)
			legacy := readTestCheckpoint(t, original)
			require.Nil(t, legacy.V3)
			upgraded, err := legacy.ToLatestVersion()
			require.NoError(t, err)
			require.NotEmpty(t, upgraded.State.PreparedClaims)
			rewritten, err := upgraded.MarshalCheckpoint()
			require.NoError(t, err)
			var projected map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(rewritten, &projected))
			delete(projected, "v3")
			if version == "v1" {
				delete(projected, "v2")
			}
			var expected map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(original, &expected))
			// Byte equality includes field order, omitted fields, nested status,
			// device/configuration layouts, and the original checksums.
			assert.Equal(t, expected, projected)
			recovered := readTestCheckpoint(t, rewritten)
			assert.Equal(t, upgraded.State, recovered.State)
		})
	}
}

func TestCheckpointV3DeviceValidation(t *testing.T) {
	valid := checkpointTestState().PreparedClaims["completed"].PreparedDevices[0].Devices[0]
	assert.True(t, validCheckpointDevice(valid))
	for name, invalid := range map[string]PreparedDevice{
		"no kind":             {},
		"multiple kinds":      {Gpu: valid.Gpu, Mig: &PreparedMigDevice{}},
		"missing GPU info":    {Gpu: &PreparedGpu{Device: valid.Gpu.Device}},
		"missing GPU device":  {Gpu: &PreparedGpu{Info: valid.Gpu.Info}},
		"empty device name":   {Gpu: &PreparedGpu{Info: valid.Gpu.Info, Device: &CheckpointedDevice{}}},
		"missing MIG tuple":   {Mig: &PreparedMigDevice{Device: valid.Gpu.Device}},
		"missing MIG device":  {Mig: &PreparedMigDevice{Concrete: &MigLiveTuple{}}},
		"missing VFIO info":   {Vfio: &PreparedVfioDevice{Device: valid.Gpu.Device}},
		"missing VFIO device": {Vfio: &PreparedVfioDevice{Info: &VfioDeviceInfo{}}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, validCheckpointDevice(invalid))
			state := &CheckpointState{PreparedClaims: PreparedClaimsByUID{"uid": {
				CheckpointState: ClaimCheckpointStatePrepareCompleted,
				PreparedDevices: PreparedDevices{{Devices: PreparedDeviceList{invalid}}},
			}}}
			v3, err := newCheckpointV3(state)
			require.NoError(t, err)
			data, err := json.Marshal(map[string]any{"v3": v3})
			require.NoError(t, err)
			var cp Checkpoint
			require.Error(t, cp.UnmarshalCheckpoint(data))
		})
	}
}

func FuzzCheckpointDecode(f *testing.F) {
	f.Add([]byte(`{"v3":{"data":"{}","checksum":0}}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"preparedClaims":{"uid":{"checkpointState":"PrepareStarted"}}}`))
	f.Add([]byte(`{"preparedClaims":{"uid":{"checkpointState":"PrepareCompleted","preparedDevices":[null]}}}`))
	state := checkpointTestState()
	payload, err := json.Marshal(state)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(payload)
	saved, err := (&Checkpoint{State: state}).MarshalCheckpoint()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(saved)
	for _, version := range []string{"v1", "v2", "v2-optional-absent"} {
		data, err := os.ReadFile(filepath.Join("testdata", "checkpoint-"+version+".json"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		exercise := func(input []byte) {
			var cp Checkpoint
			if cp.UnmarshalCheckpoint(input) != nil || cp.VerifyChecksum() != nil {
				return
			}
			latest, err := cp.ToLatestVersion()
			require.NoError(t, err)
			_, err = latest.MarshalCheckpoint()
			if cp.V3 != nil {
				require.NoError(t, err)
			}
		}
		exercise(data)
		// Also checksum each mutated payload, so fuzzing reaches typed decoding
		// and state validation instead of stopping at integrity verification.
		wrapped, err := json.Marshal(&Checkpoint{V3: &CheckpointV3{
			Data: string(data), Checksum: checksum.New(data),
		}})
		require.NoError(t, err)
		exercise(wrapped)
	})
}

func TestCheckpointManagerRestart(t *testing.T) {
	dir := t.TempDir()
	manager, err := checkpointmanager.NewCheckpointManager(dir)
	require.NoError(t, err)
	original := checkpointTestState()
	require.NoError(t, manager.CreateCheckpoint("checkpoint", &Checkpoint{State: original}))
	// A new manager has no access to any of the previous process's Go objects.
	restarted, err := checkpointmanager.NewCheckpointManager(dir)
	require.NoError(t, err)
	cp := &Checkpoint{}
	require.NoError(t, restarted.GetCheckpoint("checkpoint", cp))
	assert.Equal(t, original, cp.State)
	assert.Equal(t, original.NodeBootID, cp.GetNodeBootID())
	latest, err := cp.ToLatestVersion()
	require.NoError(t, err)
	pending := latest.State.PreparedClaims["started"]
	pending.CheckpointState = ClaimCheckpointStatePrepareCompleted
	latest.State.PreparedClaims["started"] = pending
	require.NoError(t, restarted.CreateCheckpoint("checkpoint", latest))
	recovered := &Checkpoint{}
	require.NoError(t, manager.GetCheckpoint("checkpoint", recovered))
	assert.Equal(t, latest.State, recovered.State)
}

func TestCheckpointManagerPreservesPreviousFileOnInvalidState(t *testing.T) {
	dir := t.TempDir()
	manager, err := checkpointmanager.NewCheckpointManager(dir)
	require.NoError(t, err)
	state := checkpointTestState()
	require.NoError(t, manager.CreateCheckpoint("checkpoint", &Checkpoint{State: state}))
	before, err := os.ReadFile(filepath.Join(dir, "checkpoint"))
	require.NoError(t, err)
	invalid := state.PreparedClaims["completed"]
	invalid.CheckpointState = "FutureState"
	state.PreparedClaims["completed"] = invalid
	require.ErrorContains(t, manager.CreateCheckpoint("checkpoint", &Checkpoint{State: state}), "validate checkpoint before write")
	after, err := os.ReadFile(filepath.Join(dir, "checkpoint"))
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestCheckpointV3VersionPreference(t *testing.T) {
	v3, err := newCheckpointV3(checkpointTestState())
	require.NoError(t, err)
	cp := &Checkpoint{V3: v3, V2: &CheckpointV2{NodeBootID: "legacy"}}
	latest, err := cp.ToLatestVersion()
	require.NoError(t, err)
	assert.Equal(t, "boot", latest.GetNodeBootID())
	assert.Equal(t, checkpointTestState(), latest.State)
	cp.V3.Checksum++
	_, err = cp.ToLatestVersion()
	require.Error(t, err)
	cp.V3, err = newCheckpointV3(map[string]any{"preparedClaims": map[string]any{"uid": map[string]string{"checkpointState": "FutureState"}}})
	require.NoError(t, err)
	_, err = cp.ToLatestVersion()
	require.Error(t, err)

	data, err := (&Checkpoint{State: checkpointTestState()}).MarshalCheckpoint()
	require.NoError(t, err)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &envelope))
	delete(envelope, "v3")
	envelope["V3"] = json.RawMessage(`null`)
	invalid, err := json.Marshal(envelope)
	require.NoError(t, err)
	require.Error(t, cp.UnmarshalCheckpoint(invalid))
}

// A historical writer for tests that exercise legacy-only recovery behavior.
type legacyCheckpointForTest struct{ *Checkpoint }

func (cp legacyCheckpointForTest) MarshalCheckpoint() ([]byte, error) {
	return json.Marshal(cp.Checkpoint)
}

type decodeObserver struct{ called bool }

func (o *decodeObserver) UnmarshalJSON(_ []byte) error { o.called = true; return nil }

func TestDecodeV3VerifiesBeforeTypedDecode(t *testing.T) {
	for _, payload := range []string{`{"newField":null}`, `{"nodeBootID":1}`, `{`} {
		observer := &decodeObserver{}
		raw, err := json.Marshal(CheckpointV3{Data: payload, Checksum: 0})
		require.NoError(t, err)
		_, err = decodeCheckpointV3(raw, observer)
		require.Error(t, err)
		var corrupt *cperrors.CorruptCheckpointError
		require.ErrorAs(t, err, &corrupt)
		assert.False(t, observer.called)
	}
	observer := &decodeObserver{}
	v3, err := newCheckpointV3(map[string]any{"newField": nil})
	require.NoError(t, err)
	raw, err := json.Marshal(v3)
	require.NoError(t, err)
	_, err = decodeCheckpointV3(raw, observer)
	require.NoError(t, err)
	assert.True(t, observer.called)
}

func TestV3PreservesPayloadContents(t *testing.T) {
	for name, payload := range map[string]string{
		"spacing and order":    " { \"z\": 1, \"a\": 2 }\n",
		"escapes":              `{"path":"a\\b\n\"c","html":"<>&","unicode":"π"}`,
		"missing future field": `{"DeviceName":"gpu0"}`,
		"unknown future field": `{"DeviceName":"gpu0","Metadata":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			original := &CheckpointV3{Data: payload, Checksum: checksum.New([]byte(payload))}
			raw, err := json.MarshalIndent(original, "", "  ")
			require.NoError(t, err)
			var state struct{ DeviceName string }
			got, err := decodeCheckpointV3(raw, &state)
			require.NoError(t, err)
			assert.Equal(t, original, got)
			// A changed Go projection cannot affect subsequent verification.
			require.NoError(t, got.Checksum.Verify([]byte(got.Data)))
		})
	}
}

func TestCheckpointProjection(t *testing.T) {
	type old struct {
		Name string `json:"name"`
	}
	projected, err := convertCheckpoint[old](map[string]any{"name": "device", "future": true})
	require.NoError(t, err)
	assert.Equal(t, old{Name: "device"}, projected)
	_, err = convertCheckpoint[old](make(chan int))
	require.ErrorContains(t, err, "serialize checkpoint projection")
	_, err = convertCheckpoint[old](map[string]any{"name": false})
	require.ErrorContains(t, err, "decode checkpoint projection")
	_, err = newCheckpointV3(make(chan int))
	require.ErrorContains(t, err, "serialize v3 payload")
}

func FuzzV3PayloadIntegrity(f *testing.F) {
	for _, payload := range []string{`{}`, `{"name":"gpu0"}`, `null`, `{`} {
		f.Add(payload)
	}
	f.Fuzz(func(t *testing.T, payload string) {
		original := CheckpointV3{Data: payload, Checksum: checksum.New([]byte(payload))}
		raw, err := json.Marshal(original)
		require.NoError(t, err)
		var state map[string]json.RawMessage
		got, err := decodeCheckpointV3(raw, &state)
		if err == nil {
			assert.Equal(t, original, *got)
		}
		original.Data += " "
		raw, err = json.Marshal(original)
		require.NoError(t, err)
		_, err = decodeCheckpointV3(raw, &state)
		var corrupt *cperrors.CorruptCheckpointError
		if !errors.As(err, &corrupt) {
			t.Fatalf("changed payload must fail integrity before decoding: %v", err)
		}
	})
}

func TestLegacyDeviceOptionalFields(t *testing.T) {
	for name, device := range map[string]legacyCheckpointedDevice{
		"absent":  {Requests: []string{"request"}, PoolName: "node", DeviceName: "gpu0", CDIDeviceIDs: []string{"cdi0"}},
		"present": {ShareID: new(types.UID("share")), Metadata: &legacyDeviceMetadata{}},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(device)
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &fields))
			if name == "absent" {
				assert.NotContains(t, fields, "ShareID")
				assert.NotContains(t, fields, "Metadata")
			} else {
				assert.Contains(t, fields, "ShareID")
				assert.Contains(t, fields, "Metadata")
			}
		})
	}
}
