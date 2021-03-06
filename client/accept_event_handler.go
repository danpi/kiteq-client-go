package client

import (
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/blackbeans/log4go"

	"github.com/blackbeans/kiteq-common/protocol"
	c "github.com/blackbeans/turbo/client"
	"github.com/blackbeans/turbo/packet"
	"github.com/blackbeans/turbo/pipe"
)

//接受消息事件
type acceptEvent struct {
	pipe.IForwardEvent
	msgType      uint8
	msg          interface{} //attach的数据message
	remoteClient *c.RemotingClient
	opaque       int32
}

func newAcceptEvent(msgType uint8, msg interface{}, remoteClient *c.RemotingClient, opaque int32) *acceptEvent {
	return &acceptEvent{
		msgType:      msgType,
		msg:          msg,
		opaque:       opaque,
		remoteClient: remoteClient}
}

//--------------------如下为具体的处理Handler
type AcceptHandler struct {
	pipe.BaseForwardHandler
	listener IListener
}

func NewAcceptHandler(name string, listener IListener) *AcceptHandler {
	ahandler := &AcceptHandler{}
	ahandler.BaseForwardHandler = pipe.NewBaseForwardHandler(name, ahandler)
	ahandler.listener = listener
	return ahandler
}

func (self *AcceptHandler) TypeAssert(event pipe.IEvent) bool {
	_, ok := self.cast(event)
	return ok
}

func (self *AcceptHandler) cast(event pipe.IEvent) (val *acceptEvent, ok bool) {
	val, ok = event.(*acceptEvent)

	return
}

var INVALID_MSG_TYPE_ERROR = errors.New("INVALID MSG TYPE !")

func (self *AcceptHandler) Process(ctx *pipe.DefaultPipelineContext, event pipe.IEvent) error {
	// log.DebugLog("kite_client_handler","AcceptHandler|Process|%s|%t\n", self.GetName(), event)

	acceptEvent, ok := self.cast(event)
	if !ok {
		return pipe.ERROR_INVALID_EVENT_TYPE
	}

	switch acceptEvent.msgType {
	case protocol.CMD_TX_ACK:

		//回调事务完成的监听器
		// log.DebugLog("kite_client_handler","AcceptHandler|Check Message|%t\n", acceptEvent.Msg)
		txPacket := acceptEvent.msg.(*protocol.TxACKPacket)
		header := txPacket.GetHeader()
		tx := protocol.NewTxResponse(header)
		err := self.listener.OnMessageCheck(tx)
		if nil != err {
			tx.Unknown(err.Error())
		}
		//发起一个向后的处理时间发送出去
		//填充条件
		tx.ConvertTxAckPacket(txPacket)

		txData, _ := protocol.MarshalPbMessage(txPacket)

		txResp := packet.NewRespPacket(acceptEvent.opaque, acceptEvent.msgType, txData)

		//发送提交结果确认的Packet
		remotingEvent := pipe.NewRemotingEvent(txResp, []string{acceptEvent.remoteClient.RemoteAddr()})
		ctx.SendForward(remotingEvent)
		// log.DebugLog("kite_client_handler","AcceptHandler|Recieve TXMessage|%t\n", acceptEvent.Msg)

	case protocol.CMD_STRING_MESSAGE, protocol.CMD_BYTES_MESSAGE:
		//这里应该回调消息监听器然后发送处理结果
		//log.DebugLog("kite_client_handler","AcceptHandler|Recieve Message|%t\n", acceptEvent.Msg)

		message := protocol.NewQMessage(acceptEvent.msg)

		//或者解压
		if message.GetHeader().GetSnappy() {
			switch message.GetMsgType() {
			case protocol.CMD_BYTES_MESSAGE:

				data, err := Decompress(message.GetBody().([]byte))
				if nil != err {
					log4go.ErrorLog("kite_client", "AcceptHandler|CMD_BYTES_MESSAGE|Body Decompress|FAIL|%s|%s", err, string(message.GetBody().([]byte)))
					//如果解压失败那么采用不解压
					data = message.GetBody().([]byte)
				}
				message.Body = data
			case protocol.CMD_STRING_MESSAGE:
				data, err := base64.StdEncoding.DecodeString(message.GetBody().(string))
				if nil != err {
					return fmt.Errorf("DecodeString Base64 String Fail %v", err)
				}
				data, err = Decompress(data)
				if nil != err {
					log4go.ErrorLog("kite_client", "AcceptHandler|CMD_STRING_MESSAGE|Body Decompress|FAIL|%s|%s", err, string(message.GetBody().([]byte)))
					//如果解压失败那么采用不解压
					data = message.GetBody().([]byte)
				}
				message.Body = string(data)
			}
		}

		var err error
		succ := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("%s", r)
				}
			}()
			succ = self.listener.OnMessage(message)
		}()

		dpacket := protocol.MarshalDeliverAckPacket(message.GetHeader(), succ, err)

		respPacket := packet.NewRespPacket(acceptEvent.opaque, protocol.CMD_DELIVER_ACK, dpacket)

		remotingEvent := pipe.NewRemotingEvent(respPacket, []string{acceptEvent.remoteClient.RemoteAddr()})

		ctx.SendForward(remotingEvent)

	default:
		return INVALID_MSG_TYPE_ERROR
	}

	return nil

}
