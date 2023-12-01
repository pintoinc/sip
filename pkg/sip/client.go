// Copyright 2023 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sip

import (
	"context"
	"fmt"
	"sync"

	"github.com/emiago/sipgo"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/protocol/rpc"
	"golang.org/x/exp/maps"

	"github.com/livekit/sip/pkg/config"
)

type Client struct {
	conf *config.Config

	sipCli   *sipgo.Client
	publicIp string

	cmu         sync.RWMutex
	activeCalls map[string]*outboundCall
}

func NewClient(conf *config.Config) *Client {
	c := &Client{
		conf:        conf,
		publicIp:    getPublicIP(),
		activeCalls: make(map[string]*outboundCall),
	}
	return c
}

func (c *Client) Start(agent *sipgo.UserAgent) error {
	if agent == nil {
		ua, err := sipgo.NewUA(
			sipgo.WithUserAgent(UserAgent),
		)
		if err != nil {
			return err
		}
		agent = ua
	}
	var err error
	c.sipCli, err = sipgo.NewClient(agent, sipgo.WithClientHostname(c.publicIp))
	if err != nil {
		return err
	}
	// FIXME: read existing SIP participants from the store?
	return nil
}

func (c *Client) Stop() error {
	c.cmu.Lock()
	calls := maps.Values(c.activeCalls)
	c.activeCalls = make(map[string]*outboundCall)
	c.cmu.Unlock()
	for _, call := range calls {
		call.Close()
	}
	if c.sipCli != nil {
		c.sipCli.Close()
		c.sipCli = nil
	}
	// FIXME: anything else?
	return nil
}

func (c *Client) UpdateSIPParticipant(ctx context.Context, req *rpc.InternalUpdateSIPParticipantRequest) (*rpc.InternalUpdateSIPParticipantResponse, error) {
	if req.CallTo == "" {
		logger.Infow("Disconnect SIP participant",
			"roomName", req.RoomName, "participant", req.ParticipantId)
		// Disconnect participant
		if call := c.getCall(req.ParticipantId); call != nil {
			call.Close()
		}
		return &rpc.InternalUpdateSIPParticipantResponse{}, nil
	}
	logger.Infow("Updating SIP participant",
		"roomName", req.RoomName, "participant", req.ParticipantId,
		"from", req.Number, "to", req.CallTo, "address", req.Address)
	err := c.getOrCreateCall(req.ParticipantId).Update(ctx, sipOutboundConfig{
		address: req.Address,
		from:    req.Number,
		to:      req.CallTo,
		user:    req.Username,
		pass:    req.Password,
	}, lkRoomConfig{
		roomName: req.RoomName,
		identity: req.ParticipantIdentity,
	})
	if err != nil {
		return nil, err
	}
	return &rpc.InternalUpdateSIPParticipantResponse{}, nil
}

func (c *Client) UpdateSIPParticipantAffinity(ctx context.Context, req *rpc.InternalUpdateSIPParticipantRequest) float32 {
	call := c.getCall(req.ParticipantId)
	if call != nil {
		return 1 // Existing participant
	}
	// TODO: scale affinity based on a number or active calls?
	return 0.5
}

func (c *Client) SendSIPParticipantDTMF(ctx context.Context, req *rpc.InternalSendSIPParticipantDTMFRequest) (*rpc.InternalSendSIPParticipantDTMFResponse, error) {
	call := c.getCall(req.ParticipantId)
	if call == nil {
		return nil, fmt.Errorf("Cannot send DTMF: participant not connected.")
	}
	if err := call.SendDTMF(ctx, req.Digits); err != nil {
		return nil, err
	}
	return &rpc.InternalSendSIPParticipantDTMFResponse{}, nil
}

func (c *Client) SendSIPParticipantDTMFAffinity(ctx context.Context, req *rpc.InternalSendSIPParticipantDTMFRequest) float32 {
	call := c.getCall(req.ParticipantId)
	if call != nil {
		return 1 // Only existing participants
	}
	return 0
}