// ClueGetter - Does things with mail
//
// Copyright 2016 Dolf Schimmel, Freeaqingme.
//
// This Source Code Form is subject to the terms of the two-clause BSD license.
// For its contents, please refer to the LICENSE file.
//
package bayes

import (
	"cluegetter/core"
	"fmt"
)

// TODO: HTTP Interface to report HAM/SPAM for use with e.g. Dovecot

const ModuleName = "bayes"

type module struct {
	*core.BaseModule

	cg *core.Cluegetter

	reportMessageIdRpcChan chan string
	learnMessageRpcChan    chan string
}

func init() {
	core.ModuleRegister(&module{})
}

func (m *module) Name() string {
	return ModuleName
}

func (m *module) SetCluegetter(cg *core.Cluegetter) {
	m.cg = cg
}

func (m *module) Enable() bool {
	return m.cg.Config.Bayes.Enabled
}

func (m *module) Init() {
	// TODO: Redis is not a module, yet
	//m.cg.Module("redis", "bayes") // throw error if not enabled

	m.reportMessageIdRpcChan = make(chan string, 64)
	m.learnMessageRpcChan = make(chan string, 64)

	go m.reportMessageIdQueue(m.reportMessageIdRpcChan)
	go m.handleLearnQueue(m.learnMessageRpcChan)
}

func (m *module) Rpc() map[string]chan string {
	return map[string]chan string{
		"bayes!reportMessageId": m.reportMessageIdRpcChan,
		"bayes!learn":           m.learnMessageRpcChan,
	}
}

func (m *module) reportMessageIdQueue(reportMessageIdQueue chan string) {
	for report := range reportMessageIdQueue {
		go m.handleReportMessageIdQueueItem(report)
	}
}

func (m *module) handleLearnQueue(learnMessageQueue chan string) {
	for lesson := range learnMessageQueue {
		go m.learn(lesson)
	}
}

func (m *module) handleReportMessageIdQueueItem(item string) {
	core.CluegetterRecover("bayesHandleReportMessageIdQueueItem")

	rpc := &core.Rpc{}
	err := rpc.Unmarshal([]byte(item))
	if err != nil {
		m.cg.Log.Errorf("Could not unmarshal RPC Message Bayes_Learn_Message_Id:", err.Error())
		return
	}

	if rpc.Name != "Bayes_Learn_Message_Id" || rpc.Bayes_Learn_Message_Id == nil {
		m.cg.Log.Errorf("Invalid RPC Message Bayes_Learn_Message_Id")
		return
	}
	rpcMsg := rpc.Bayes_Learn_Message_Id

	msgBytes := core.MessagePersistCache.GetByMessageId(rpcMsg.MessageId)
	if len(msgBytes) == 0 {
		m.cg.Log.Errorf("Could not retrieve message from cache with message-id %s",
			rpcMsg.MessageId)
		return
	}

	msg, err := core.MessagePersistUnmarshalProto(msgBytes)
	if err != nil {
		m.cg.Log.Errorf("Could not unmarshal message from cache: %s", err.Error())
		return
	}
	rpcName := "Bayes_Learn_Message"
	rpcOut := &core.Rpc{
		Name: rpcName,
		Bayes_Learn_Message: &core.Rpc__Bayes_Learn_Message{
			IsSpam:   rpcMsg.IsSpam,
			Message:  msg,
			Host:     rpcMsg.Host,
			Reporter: rpcMsg.Reporter,
			Reason:   rpcMsg.Reason,
		},
	}

	if rpcMsg.IsSpam {
		bayesAddToCorpus(true, msg, rpcMsg.MessageId, rpcMsg.Host, rpcMsg.Reporter, rpcMsg.Reason)
	} else {
		bayesAddToCorpus(false, msg, rpcMsg.MessageId, rpcMsg.Host, rpcMsg.Reporter, rpcMsg.Reason)
	}

	payload, err := rpcOut.Marshal()
	if err != nil {
		m.cg.Log.Errorf("Could not marshal data-object to json: %s", err.Error())
		return
	}
	// TODO: redis := m.cg.Module("redis", "")
	err = core.RedisPublish(fmt.Sprintf("cluegetter!!bayes!learn"), payload)
	if err != nil {
		m.cg.Log.Errorf("Error while reporting bayes message id: %s", err.Error())
	}
}

func bayesAddToCorpus(spam bool, msg *core.Proto_Message, messageId, host, reporter, reason string) {
	// TODO
}

func (m *module) ReportMessageId(spam bool, messageId, host, reporter, reason string) {
	core.CluegetterRecover("bayes.reportMessageId")
	if !m.cg.Config.Bayes.Enabled {
		return
	}

	rpcName := "Bayes_Learn_Message_Id"
	payload := &core.Rpc{
		Name: rpcName,
		Bayes_Learn_Message_Id: &core.Rpc__Bayes_Learn_Message_Id{
			IsSpam:    spam,
			MessageId: messageId,
			Host:      host,
			Reporter:  reporter,
			Reason:    reason,
		},
	}

	key := fmt.Sprintf("cluegetter!%d!bayes!reportMessageId", m.cg.Instance())
	payloadBytes, _ := payload.Marshal()
	err := core.RedisPublish(key, payloadBytes)

	if err != nil {
		m.cg.Log.Errorf("Error while reporting bayes message id: %s", err.Error())
	}
}

func (m *module) learn(item string) {
	rpc := &core.Rpc{}
	err := rpc.Unmarshal([]byte(item))
	if err != nil {
		m.cg.Log.Errorf("Could not unmarshal RPC Message Bayes_Learn_Message:", err.Error())
		return
	}

	if rpc.Name != "Bayes_Learn_Message" || rpc.Bayes_Learn_Message == nil {
		m.cg.Log.Errorf("Invalid RPC Message Bayes_Learn_Message")
		return
	}

	msg := rpc.Bayes_Learn_Message.Message.GetAsMessage()
	for _, module := range m.cg.Modules() {
		go func(m core.Module, msg *core.Message, isSpam bool) {
			core.CluegetterRecover("bayesLearn." + m.Name())
			module.BayesLearn(msg, isSpam)
		}(module, msg, rpc.Bayes_Learn_Message.IsSpam)
	}
}
