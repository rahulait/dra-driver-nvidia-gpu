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
	"bytes"
	"encoding/json"
	"fmt"

	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"

	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/checksum"
)

// CheckpointedDevice follows the current kubelet device type. Legacy copies use
// legacyCheckpointedDevice so upstream fields cannot change their JSON or checksums.
type CheckpointedDevice kubeletplugin.Device

type ClaimCheckpointState string

const (
	ClaimCheckpointStateUnset            ClaimCheckpointState = ""
	ClaimCheckpointStatePrepareStarted   ClaimCheckpointState = "PrepareStarted"
	ClaimCheckpointStatePrepareCompleted ClaimCheckpointState = "PrepareCompleted"
	ClaimCheckpointStatePrepareAborted   ClaimCheckpointState = "PrepareAborted"
)

// Latest version type aliases

type PreparedClaimsByUID = PreparedClaimsByUIDV3
type PreparedClaim = PreparedClaimV3

// CheckpointV3 keeps the original payload independent of the reader's working-state types.
type CheckpointV3 struct {
	Data     string            `json:"data"`
	Checksum checksum.Checksum `json:"checksum"`
}

// V3 working state is independent of the frozen compatibility projections.
type CheckpointState struct {
	PreparedClaims PreparedClaimsByUIDV3 `json:"preparedClaims,omitempty"`
	NodeBootID     string                `json:"nodeBootID,omitempty"`
}

type PreparedClaimsByUIDV3 map[string]PreparedClaimV3

type PreparedClaimV3 struct {
	CheckpointState ClaimCheckpointState            `json:"checkpointState"`
	Status          resourceapi.ResourceClaimStatus `json:"status"`
	PreparedDevices PreparedDevices                 `json:"preparedDevices,omitempty"`
	Name            string                          `json:"name,omitempty"`
	Namespace       string                          `json:"namespace,omitempty"`
	AbortedAt       *metav1.Time                    `json:"abortedAt,omitempty"`
}

// V2 types

type CheckpointV2 struct {
	Checksum       checksum.Checksum     `json:"checksum"`
	PreparedClaims PreparedClaimsByUIDV2 `json:"preparedClaims,omitempty"`
	// NodeBootID is the Linux kernel boot_id
	// for the node when this checkpoint was last validated at plugin startup.
	// If it differs from the current boot id, prepared claims are invalid (reboot).
	NodeBootID string `json:"nodeBootID,omitempty"`
}

type PreparedClaimsByUIDV2 map[string]PreparedClaimV2

type PreparedClaimV2 struct {
	CheckpointState ClaimCheckpointState      `json:"checkpointState"`
	Status          legacyResourceClaimStatus `json:"status,omitempty"`
	PreparedDevices legacyPreparedDevices     `json:"preparedDevices,omitempty"`
	Name            string                    `json:"name,omitempty"`
	Namespace       string                    `json:"namespace,omitempty"`
	AbortedAt       *metav1.Time              `json:"abortedAt,omitempty"`
}

// V1 types

type CheckpointV1 struct {
	PreparedClaims PreparedClaimsByUIDV1 `json:"preparedClaims,omitempty"`
}

type PreparedClaimsByUIDV1 map[string]PreparedClaimV1

type PreparedClaimV1 struct {
	Status          legacyResourceClaimStatus `json:"status,omitempty"`
	PreparedDevices legacyPreparedDevices     `json:"preparedDevices,omitempty"`
}

// Conversion functions

func (v1 *CheckpointV1) ToV2() *CheckpointV2 {
	v2 := &CheckpointV2{
		PreparedClaims: make(PreparedClaimsByUIDV2),
	}
	for claimUID, v1Claim := range v1.PreparedClaims {
		v2.PreparedClaims[claimUID] = PreparedClaimV2{
			CheckpointState: ClaimCheckpointStatePrepareCompleted,
			Status:          v1Claim.Status,
			PreparedDevices: v1Claim.PreparedDevices,
		}
	}
	return v2
}

func (v2 *CheckpointV2) ToV1() *CheckpointV1 {
	v1 := &CheckpointV1{
		PreparedClaims: make(PreparedClaimsByUIDV1),
	}
	for claimUID, v1Claim := range v2.PreparedClaims {
		if v1Claim.CheckpointState != ClaimCheckpointStatePrepareCompleted {
			continue
		}
		v1.PreparedClaims[claimUID] = PreparedClaimV1{
			Status:          v1Claim.Status,
			PreparedDevices: v1Claim.PreparedDevices,
		}
	}
	return v1
}

// Validate rejects states that recovery cannot interpret safely. Unknown JSON
// fields remain allowed when they can be ignored without changing recovery.
func (state *CheckpointState) Validate() error {
	for uid, claim := range state.PreparedClaims {
		if uid == "" {
			return fmt.Errorf("claim UID must not be empty")
		}
		switch claim.CheckpointState {
		case ClaimCheckpointStatePrepareStarted, ClaimCheckpointStatePrepareCompleted, ClaimCheckpointStatePrepareAborted:
		default:
			return fmt.Errorf("claim %q has unsupported preparation state %q", uid, claim.CheckpointState)
		}
		for _, group := range claim.PreparedDevices {
			if group == nil {
				return fmt.Errorf("claim %q has a null device group", uid)
			}
			for _, device := range group.Devices {
				if !validCheckpointDevice(device) {
					return fmt.Errorf("claim %q has an invalid prepared device", uid)
				}
			}
		}
	}
	return nil
}

func validCheckpointDevice(device PreparedDevice) bool {
	kinds := 0
	var prepared *CheckpointedDevice
	if device.Channel != nil {
		kinds++
		if device.Channel.Info == nil {
			return false
		}
		prepared = device.Channel.Device
	}
	if device.Daemon != nil {
		kinds++
		if device.Daemon.Info == nil {
			return false
		}
		prepared = device.Daemon.Device
	}
	return kinds == 1 && prepared != nil && prepared.DeviceName != ""
}

// newCheckpointV3 serializes state once, then checksums those exact payload bytes.
func newCheckpointV3(state any) (*CheckpointV3, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("serialize v3 payload: %w", err)
	}
	return &CheckpointV3{Data: string(data), Checksum: checksum.New(data)}, nil
}

// decodeCheckpointV3 verifies integrity before allowing any typed payload decoding.
func decodeCheckpointV3(raw json.RawMessage, state any) (*CheckpointV3, error) {
	var wire struct {
		Data     *string            `json:"data"`
		Checksum *checksum.Checksum `json:"checksum"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("decode v3 envelope: %w", err)
	}
	if wire.Data == nil || wire.Checksum == nil {
		return nil, fmt.Errorf("v3 requires non-null data and checksum fields")
	}
	v3 := &CheckpointV3{Data: *wire.Data, Checksum: *wire.Checksum}
	if err := v3.decode(state); err != nil {
		return nil, err
	}
	return v3, nil
}

// decode verifies the original payload before decoding it into working state.
func (v3 *CheckpointV3) decode(state any) error {
	if err := v3.Checksum.Verify([]byte(v3.Data)); err != nil {
		return fmt.Errorf("verify v3 payload: %w", err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace([]byte(v3.Data)), []byte("{")) {
		return fmt.Errorf("v3 payload must be a JSON object")
	}
	if err := json.Unmarshal([]byte(v3.Data), state); err != nil {
		return fmt.Errorf("decode v3 payload: %w", err)
	}
	return nil
}

// convertCheckpoint projects JSON-compatible state into an independent versioned schema.
// Unknown fields are intentionally omitted when projecting into a legacy schema.
func convertCheckpoint[T any](source any) (T, error) {
	var target T
	data, err := json.Marshal(source)
	if err != nil {
		return target, fmt.Errorf("serialize checkpoint projection: %w", err)
	}
	if err := json.Unmarshal(data, &target); err != nil {
		return target, fmt.Errorf("decode checkpoint projection: %w", err)
	}
	return target, nil
}
