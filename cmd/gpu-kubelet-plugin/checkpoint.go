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
	"fmt"

	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/checksum"
)

type Checkpoint struct {
	// Note: The Checksum below is only associated with the V1 checkpoint
	// (because it doesn't have an embedded one). All future versions have
	// their checksum directly embedded in them to better support
	// downgrades. This checksum will be removed once the V1 checkpoint is
	// no longer supported.
	Checksum checksum.Checksum `json:"checksum"`
	V1       *CheckpointV1     `json:"v1,omitempty"`
	V2       *CheckpointV2     `json:"v2,omitempty"`
	V3       *CheckpointV3     `json:"v3,omitempty"`
	State    *CheckpointState  `json:"-"`
}

func (cp *Checkpoint) ToLatestVersion() (*Checkpoint, error) {
	state := cp.State
	if state == nil && cp.V3 != nil {
		state = &CheckpointState{}
		if err := cp.V3.decode(state); err != nil {
			return nil, err
		}
		if err := state.Validate(); err != nil {
			return nil, fmt.Errorf("validate v3 state: %w", err)
		}
	}
	if state == nil {
		v2 := cp.V2
		if v2 == nil && cp.V1 != nil {
			v2 = cp.V1.ToV2()
		}
		state = &CheckpointState{}
		if v2 != nil {
			converted, err := convertCheckpoint[CheckpointState](v2)
			if err != nil {
				return nil, fmt.Errorf("convert legacy checkpoint: %w", err)
			}
			state = &converted
		}
	}
	if state.PreparedClaims == nil {
		state.PreparedClaims = make(PreparedClaimsByUID)
	}
	return &Checkpoint{State: state}, nil
}

func (cp *Checkpoint) MarshalCheckpoint() ([]byte, error) {
	latest, err := cp.ToLatestVersion()
	if err != nil {
		return nil, err
	}
	if err := latest.State.Validate(); err != nil {
		return nil, fmt.Errorf("validate checkpoint before write: %w", err)
	}
	// Capture the working state once before constructing any compatibility copy.
	v3, err := newCheckpointV3(latest.State)
	if err != nil {
		return nil, err
	}
	v2, err := convertCheckpoint[CheckpointV2](json.RawMessage(v3.Data))
	if err != nil {
		return nil, fmt.Errorf("project v2 checkpoint: %w", err)
	}
	out := &Checkpoint{V3: v3, V2: &v2, V1: v2.ToV1()}
	if err := out.SetChecksumV1(); err != nil {
		return nil, fmt.Errorf("set v1 checksum: %w", err)
	}
	if err := out.SetChecksumV2(); err != nil {
		return nil, fmt.Errorf("set v2 checksum: %w", err)
	}
	return json.Marshal(out)
}

// checksumDataV1 deliberately excludes all other versions and working state.
func (cp *Checkpoint) checksumDataV1() ([]byte, error) {
	return json.Marshal(struct {
		Checksum checksum.Checksum `json:"checksum"`
		V1       *CheckpointV1     `json:"v1,omitempty"`
	}{V1: cp.V1})
}

func (cp *Checkpoint) SetChecksumV1() error {
	data, err := cp.checksumDataV1()
	if err != nil {
		return err
	}
	cp.Checksum = checksum.New(data)
	return nil
}

func (cp *Checkpoint) SetChecksumV2() error {
	cp.V2.Checksum = 0
	out, err := json.Marshal(*cp.V2)
	if err != nil {
		return err
	}
	cp.V2.Checksum = checksum.New(out)
	return nil
}

func (cp *Checkpoint) UnmarshalCheckpoint(data []byte) error {
	// Raw sections prevent malformed or evolving legacy types from interfering
	// with V3 recovery. Presence must distinguish an absent V3 from explicit null.
	var envelope struct {
		Checksum json.RawMessage `json:"checksum"`
		V1       json.RawMessage `json:"v1"`
		V2       json.RawMessage `json:"v2"`
		V3       json.RawMessage `json:"v3"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode checkpoint envelope: %w", err)
	}
	if envelope.V3 != nil {
		var state CheckpointState
		v3, err := decodeCheckpointV3(envelope.V3, &state)
		if err != nil {
			return err
		}
		if err := state.Validate(); err != nil {
			return fmt.Errorf("validate v3 state: %w", err)
		}
		*cp = Checkpoint{V3: v3, State: &state}
		return nil
	}
	type legacyCheckpoint Checkpoint
	var legacy legacyCheckpoint
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("decode legacy checkpoint: %w", err)
	}
	if legacy.V1 == nil && legacy.V2 == nil {
		return fmt.Errorf("checkpoint has no supported version")
	}
	*cp = Checkpoint(legacy)
	return nil
}

func (cp *Checkpoint) VerifyChecksum() error {
	if cp.V3 != nil {
		return cp.V3.Checksum.Verify([]byte(cp.V3.Data))
	}
	if err := cp.VerifyChecksumV1(); err != nil {
		return err
	}
	return cp.VerifyChecksumV2()
}

func (cp *Checkpoint) VerifyChecksumV1() error {
	data, err := cp.checksumDataV1()
	if err != nil {
		return err
	}
	return cp.Checksum.Verify(data)
}

func (cp *Checkpoint) VerifyChecksumV2() error {
	if cp.V2 == nil {
		return nil
	}

	ck := cp.V2.Checksum
	defer func() {
		cp.V2.Checksum = ck
	}()
	cp.V2.Checksum = 0
	out, err := json.Marshal(*cp.V2)
	if err != nil {
		return err
	}
	return ck.Verify(out)
}

func (cp *Checkpoint) GetNodeBootID() string {
	if cp.State != nil {
		return cp.State.NodeBootID
	}
	if cp.V2 != nil {
		return cp.V2.NodeBootID
	}
	return ""
}
