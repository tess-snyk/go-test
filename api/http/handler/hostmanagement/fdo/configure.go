package fdo

import (
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	cbor "github.com/fxamacker/cbor/v2"

	httperror "github.com/portainer/libhttp/error"
	"github.com/portainer/libhttp/request"
	"github.com/portainer/libhttp/response"
	"github.com/sirupsen/logrus"
)

type deviceConfigurePayload struct {
	EdgeKey string `json:"edgekey"`
	Name    string `json:"name"`
	Profile int    `json:"profile"`
}

func (payload *deviceConfigurePayload) Validate(r *http.Request) error {
	if payload.EdgeKey == "" {
		return errors.New("invalid edge key provided")
	}

	if payload.Name == "" {
		return errors.New("the device name cannot be empty")
	}

	return nil
}

// @id fdoConfigureDevice
// @summary configure an FDO device
// @description configure an FDO device
// @description **Access policy**: administrator
// @tags intel
// @security jwt
// @produce json
// @param body body deviceConfigurePayload true "Device Configuration"
// @success 200 "Success"
// @failure 400 "Invalid request"
// @failure 403 "Permission denied to access settings"
// @failure 500 "Server error"
// @router /fdo/configure/{guid} [post]
func (handler *Handler) fdoConfigureDevice(w http.ResponseWriter, r *http.Request) *httperror.HandlerError {
	guid, err := request.RetrieveRouteVariableValue(r, "guid")
	if err != nil {
		logrus.WithError(err).Info("fdoConfigureDevice: request.RetrieveRouteVariableValue()")
		return &httperror.HandlerError{StatusCode: http.StatusInternalServerError, Message: "fdoConfigureDevice: guid not found", Err: err}
	}

	var payload deviceConfigurePayload

	err = request.DecodeAndValidateJSONPayload(r, &payload)
	if err != nil {
		logrus.WithError(err).Error("Invalid request payload")
		return &httperror.HandlerError{StatusCode: http.StatusBadRequest, Message: "Invalid request payload", Err: err}
	}

	fdoClient, err := handler.newFDOClient()
	if err != nil {
		logrus.WithError(err).Info("fdoConfigureDevice: newFDOClient()")
		return &httperror.HandlerError{StatusCode: http.StatusInternalServerError, Message: "fdoConfigureDevice: newFDOClient()", Err: err}
	}

	// enable fdo_sys
	if err = fdoClient.PutDeviceSVIRaw(url.Values{
		"guid":     []string{guid},
		"priority": []string{"0"},
		"module":   []string{"fdo_sys"},
		"var":      []string{"active"},
		"bytes":    []string{"F5"}, // this is "true" in CBOR
	}, []byte("")); err != nil {
		logrus.WithError(err).Info("fdoRegisterDevice: PutDeviceSVI()")
		return &httperror.HandlerError{StatusCode: http.StatusInternalServerError, Message: "fdoRegisterDevice: PutDeviceSVI()", Err: err}
	}
	// write down the edgekey
	if err = fdoClient.PutDeviceSVIRaw(url.Values{
		"guid":     []string{guid},
		"priority": []string{"1"},
		"module":   []string{"fdo_sys"},
		"var":      []string{"filedesc"},
		"filename": []string{"DEVICE_edgekey.txt"},
	}, []byte(payload.EdgeKey)); err != nil {
		logrus.WithError(err).Info("fdoRegisterDevice: PutDeviceSVI(edgekey)")
		return &httperror.HandlerError{StatusCode: http.StatusInternalServerError, Message: "fdoRegisterDevice: PutDeviceSVI(edgekey)", Err: err}
	}
	// write down the device name
	if err = fdoClient.PutDeviceSVIRaw(url.Values{
		"guid":     []string{guid},
		"priority": []string{"1"},
		"module":   []string{"fdo_sys"},
		"var":      []string{"filedesc"},
		"filename": []string{"DEVICE_name.txt"},
	}, []byte(payload.Name)); err != nil {
		logrus.WithError(err).Info("fdoRegisterDevice: PutDeviceSVI(name)")
		return &httperror.HandlerError{StatusCode: http.StatusInternalServerError, Message: "fdoRegisterDevice: PutDeviceSVI(name)", Err: err}
	}
	// write down the device GUID - used as the EDGE_DEVICE_GUID too
	if err = fdoClient.PutDeviceSVIRaw(url.Values{
		"guid":     []string{guid},
		"priority": []string{"1"},
		"module":   []string{"fdo_sys"},
		"var":      []string{"filedesc"},
		"filename": []string{"DEVICE_GUID.txt"},
	}, []byte(guid)); err != nil {
		logrus.WithError(err).Info("fdoRegisterDevice: PutDeviceSVI()")
		return &httperror.HandlerError{StatusCode: http.StatusInternalServerError, Message: "fdoRegisterDevice: PutDeviceSVI()", Err: err}
	}

	// onboarding script - this would get selected by the profile name
	deploymentScriptName := "portainer.sh"
	if err = fdoClient.PutDeviceSVIRaw(url.Values{
		"guid":     []string{guid},
		"priority": []string{"1"},
		"module":   []string{"fdo_sys"},
		"var":      []string{"filedesc"},
		"filename": []string{deploymentScriptName},
	}, []byte(`#!/bin/bash -ex
# deploying `+strconv.Itoa(payload.Profile)+`
env > env.log

export AGENT_IMAGE=portainer/agent:2.9.3
export GUID=$(cat DEVICE_GUID.txt)
export DEVICE_NAME=$(cat DEVICE_name.txt)
export EDGE_KEY=$(cat DEVICE_edgekey.txt)
export AGENTVOLUME=$(pwd)/data/portainer_agent_data/

mkdir -p ${AGENTVOLUME}

docker pull ${AGENT_IMAGE}

docker run -d \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v /var/lib/docker/volumes:/var/lib/docker/volumes \
    -v /:/host \
    -v ${AGENTVOLUME}:/data \
    --restart always \
    -e EDGE=1 \
    -e EDGE_ID=${GUID} \
    -e EDGE_KEY=${EDGE_KEY} \
    -e CAP_HOST_MANAGEMENT=1 \
    -e EDGE_INSECURE_POLL=1 \
    --name portainer_edge_agent \
    ${AGENT_IMAGE}
`)); err != nil {
		logrus.WithError(err).Info("fdoRegisterDevice: PutDeviceSVI()")
		return &httperror.HandlerError{StatusCode: http.StatusInternalServerError, Message: "fdoRegisterDevice: PutDeviceSVI()", Err: err}
	}

	b, err := cbor.Marshal([]string{"/bin/sh", deploymentScriptName})

	if err != nil {
		logrus.WithError(err).Error("failed to marshal string to CBOR")
		return &httperror.HandlerError{StatusCode: http.StatusInternalServerError, Message: "fdoRegisterDevice: PutDeviceSVI() failed to encode", Err: err}

	}

	cbor := strings.ToUpper(hex.EncodeToString(b))
	logrus.WithField("cbor", cbor).WithField("string", deploymentScriptName).Info("converted to CBOR")

	if err = fdoClient.PutDeviceSVIRaw(url.Values{
		"guid":     []string{guid},
		"priority": []string{"2"},
		"module":   []string{"fdo_sys"},
		"var":      []string{"exec"},
		"bytes":    []string{cbor},
	}, []byte("")); err != nil {
		logrus.WithError(err).Info("fdoRegisterDevice: PutDeviceSVI()")
		return &httperror.HandlerError{StatusCode: http.StatusInternalServerError, Message: "fdoRegisterDevice: PutDeviceSVI()", Err: err}
	}

	return response.Empty(w)
}