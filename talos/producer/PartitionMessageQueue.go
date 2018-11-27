/**
 * Copyright 2018, Xiaomi.
 * All rights reserved.
 * Author: wangfan8@xiaomi.com
 */

package producer

import (
	"github.com/XiaoMi/talos-sdk-golang/talos/thrift/message"
	"github.com/XiaoMi/talos-sdk-golang/talos/utils"
	"github.com/alecthomas/log4go"
	"sync"
	"time"
)

type PartitionMessageQueue struct {
	userMessageList []*UserMessage // or list
	curMessageBytes int64
	partitionId     int32
	talosProducer   *TalosProducer
	maxBufferedTime int64
	maxPutMsgNumber int64
	maxPutMsgBytes  int64
	mu              sync.Mutex
}

func NewPartitionMessageQueue(producerConfig *TalosProducerConfig,
	partitionId int32, talosProducer *TalosProducer) *PartitionMessageQueue {

	return &PartitionMessageQueue{
		userMessageList: make([]*UserMessage, 0),
		curMessageBytes: 0,
		partitionId:     partitionId,
		talosProducer:   talosProducer,
		maxBufferedTime: producerConfig.GetMaxBufferedMsgTime(),
		maxPutMsgNumber: producerConfig.GetMaxPutMsgNumber(),
		maxPutMsgBytes:  producerConfig.GetMaxPutMsgBytes(),
	}
}

func (q *PartitionMessageQueue) AddMessage(messageList []*UserMessage) {
	q.mu.Lock()
	incrementBytes := int64(0)
	for _, userMessage := range messageList {
		q.userMessageList = append(q.userMessageList, userMessage)
		incrementBytes += userMessage.GetMessageSize()
	}
	q.curMessageBytes += int64(incrementBytes)
	// update total buffered count when add messageList
	q.talosProducer.increaseBufferedCount(int64(len(messageList)),
		int64(incrementBytes))
	// TODO:
	// notify partitionSender to getUserMessageList
	q.mu.Unlock()
}

/**
 * return messageList, if not shouldPut, block in this method
 */
func (q *PartitionMessageQueue) GetMessageList() []*message.Message {
	q.mu.Lock()
	for !q.shouldPut() {
		waitTime := q.getWaitTime()
		//TODO : make sure == wait()
		time.Sleep(time.Duration(waitTime))
	}
	log4go.Debug("getUserMessageList wake up for partition: %d", q.partitionId)

	returnList := make([]*message.Message, 0)
	returnMsgBytes, returnMsgNumber := int64(0), int64(0)
	for len(q.userMessageList) > 0 &&
		returnMsgNumber < q.maxPutMsgNumber &&
		returnMsgBytes < q.maxPutMsgBytes {
		userMessage := q.userMessageList[0]
		q.userMessageList = q.userMessageList[1:]
		returnList = append(returnList, userMessage.GetMessage())
		q.curMessageBytes -= userMessage.GetMessageSize()
		returnMsgBytes += userMessage.GetMessageSize()
		returnMsgNumber++
	}

	// update total buffered count when poll messageList
	q.talosProducer.decreaseBufferedCount(returnMsgNumber, returnMsgBytes)
	log4go.Info("Ready to put message batch: %d, queue size: %d and curBytes: %d"+
		" for partition: %d", len(returnList), len(q.userMessageList),
		q.curMessageBytes, q.partitionId)
	q.mu.Unlock()
	return returnList
}

func (q *PartitionMessageQueue) shouldPut() bool {
	// when TalosProducer is not active;
	if !q.talosProducer.IsActive() {
		return true
	}

	// when we have enough bytes data or enough number data;
	if q.curMessageBytes >= q.maxPutMsgBytes ||
		int64(len(q.userMessageList)) >= q.maxPutMsgNumber {
		return true
	}

	// when there have at least one message and it has exist enough long time;
	if len(q.userMessageList) > 0 && (utils.CurrentTimeMills()-
		q.userMessageList[0].GetTimestamp()) >= q.maxBufferedTime {
		return true
	}
	return false
}

/**
 * Note: wait(0) represents wait infinite until be notified
 * so we wait minimal 1 milli secs when time <= 0
 */

func (q *PartitionMessageQueue) getWaitTime() int64 {
	if len(q.userMessageList) <= 0 {
		return 0
	}
	time := q.userMessageList[0].GetTimestamp() + q.maxBufferedTime - utils.CurrentTimeMills()
	if time > 0 {
		return time
	} else {
		return 1
	}
}
