/*
Copyright The Kubernetes Authors.

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

package main

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// Frozen V1/V2 device and configuration layouts. Do not add working-state fields.
type legacyPreparedDevices []*legacyPreparedDeviceGroup
type legacyPreparedDeviceGroup struct {
	Devices     []legacyPreparedDevice  `json:"devices"`
	ConfigState legacyDeviceConfigState `json:"configState"`
}

type legacyPreparedDevice struct {
	Channel *legacyPreparedComputeDomain `json:"channel"`
	Daemon  *legacyPreparedComputeDomain `json:"daemon"`
}
type legacyPreparedComputeDomain struct {
	Info   *legacyComputeDomainInfo  `json:"info"`
	Device *legacyCheckpointedDevice `json:"device"`
}
type legacyComputeDomainInfo struct {
	ID int `json:"id"`
}
type legacyDeviceConfigState struct {
	Type          string
	ComputeDomain string
}

// These types freeze the V1/V2 JSON contract. Keep their fields, order, and tags
// stable even when working state or upstream Kubernetes types change.
type legacyResourceClaimStatus struct {
	Allocation  *legacyAllocationResult                `json:"allocation,omitempty"`
	ReservedFor []legacyResourceClaimConsumerReference `json:"reservedFor,omitempty"`
	Devices     []legacyAllocatedDeviceStatus          `json:"devices,omitempty"`
}

type legacyAllocationResult struct {
	Devices             legacyDeviceAllocationResult `json:"devices,omitempty"`
	NodeSelector        *legacyNodeSelector          `json:"nodeSelector,omitempty"`
	AllocationTimestamp *metav1.Time                 `json:"allocationTimestamp,omitempty"`
}

type legacyDeviceAllocationResult struct {
	Results []legacyDeviceRequestAllocationResult `json:"results,omitempty"`
	Config  []legacyDeviceAllocationConfiguration `json:"config,omitempty"`
}

type legacyDeviceRequestAllocationResult struct {
	Request                  string                                    `json:"request"`
	Driver                   string                                    `json:"driver"`
	Pool                     string                                    `json:"pool"`
	Device                   string                                    `json:"device"`
	AdminAccess              *bool                                     `json:"adminAccess,omitempty"`
	Tolerations              []legacyDeviceToleration                  `json:"tolerations,omitempty"`
	BindingConditions        []string                                  `json:"bindingConditions,omitempty"`
	BindingFailureConditions []string                                  `json:"bindingFailureConditions,omitempty"`
	ShareID                  *types.UID                                `json:"shareID,omitempty"`
	ConsumedCapacity         map[legacyQualifiedName]resource.Quantity `json:"consumedCapacity,omitempty"`
	SkipNodeOperations       []legacySkipNodeOperation                 `json:"skipNodeOperations,omitempty"`
}

type legacyDeviceToleration struct {
	Key               string                         `json:"key,omitempty"`
	Operator          legacyDeviceTolerationOperator `json:"operator,omitempty"`
	Value             string                         `json:"value,omitempty"`
	Effect            legacyDeviceTaintEffect        `json:"effect,omitempty"`
	TolerationSeconds *int64                         `json:"tolerationSeconds,omitempty"`
}

type legacyDeviceTolerationOperator string

type legacyDeviceTaintEffect string

type legacyQualifiedName string

type legacySkipNodeOperation string

type legacyDeviceAllocationConfiguration struct {
	Source                    legacyAllocationConfigSource `json:"source"`
	Requests                  []string                     `json:"requests,omitempty"`
	legacyDeviceConfiguration `json:""`
}

type legacyAllocationConfigSource string

type legacyDeviceConfiguration struct {
	Opaque *legacyOpaqueDeviceConfiguration `json:"opaque,omitempty"`
}

type legacyOpaqueDeviceConfiguration struct {
	Driver     string               `json:"driver"`
	Parameters runtime.RawExtension `json:"parameters"`
}

type legacyResourceClaimConsumerReference struct {
	APIGroup string    `json:"apiGroup,omitempty"`
	Resource string    `json:"resource"`
	Name     string    `json:"name"`
	UID      types.UID `json:"uid"`
}

type legacyAllocatedDeviceStatus struct {
	Driver      string                   `json:"driver"`
	Pool        string                   `json:"pool"`
	Device      string                   `json:"device"`
	ShareID     *string                  `json:"shareID,omitempty"`
	Conditions  []legacyCondition        `json:"conditions"`
	Data        *runtime.RawExtension    `json:"data,omitempty"`
	NetworkData *legacyNetworkDeviceData `json:"networkData,omitempty"`
}

type legacyNetworkDeviceData struct {
	InterfaceName   string   `json:"interfaceName,omitempty"`
	IPs             []string `json:"ips,omitempty"`
	HardwareAddress string   `json:"hardwareAddress,omitempty"`
}

type legacyDeviceAttribute struct {
	IntValue      *int64   `json:"int,omitempty"`
	BoolValue     *bool    `json:"bool,omitempty"`
	StringValue   *string  `json:"string,omitempty"`
	VersionValue  *string  `json:"version,omitempty"`
	IntValues     []int64  `json:"ints,omitempty"`
	BoolValues    []bool   `json:"bools,omitempty"`
	StringValues  []string `json:"strings,omitempty"`
	VersionValues []string `json:"versions,omitempty"`
}

// legacyNodeSelector and legacyCondition freeze nested API objects covered by legacy checksums.
type legacyNodeSelector struct {
	NodeSelectorTerms []legacyNodeSelectorTerm `json:"nodeSelectorTerms"`
}
type legacyNodeSelectorTerm struct {
	MatchExpressions []legacyNodeSelectorRequirement `json:"matchExpressions,omitempty"`
	MatchFields      []legacyNodeSelectorRequirement `json:"matchFields,omitempty"`
}
type legacyNodeSelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}
type legacyCondition struct {
	Type               string      `json:"type"`
	Status             string      `json:"status"`
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`
	Reason             string      `json:"reason"`
	Message            string      `json:"message"`
}

// legacyCheckpointedDevice freezes kubeletplugin.Device's legacy JSON, including optional fields.
type legacyCheckpointedDevice struct {
	Requests     []string              `json:"Requests"`
	PoolName     string                `json:"PoolName"`
	DeviceName   string                `json:"DeviceName"`
	CDIDeviceIDs []string              `json:"CDIDeviceIDs"`
	ShareID      *types.UID            `json:"ShareID,omitempty"`
	Metadata     *legacyDeviceMetadata `json:"Metadata,omitempty"`
}
type legacyDeviceMetadata struct {
	Attributes  map[string]legacyDeviceAttribute
	NetworkData *legacyNetworkDeviceData
}
