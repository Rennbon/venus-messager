package service

import (
	"context"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"sort"
	"strings"
	"sync"
	"time"

	venusTypes "github.com/filecoin-project/venus/pkg/types"
	"github.com/ipfs-force-community/venus-wallet/core"
	"github.com/sirupsen/logrus"
	"golang.org/x/xerrors"

	"github.com/ipfs-force-community/venus-messager/config"
	"github.com/ipfs-force-community/venus-messager/models/repo"
	"github.com/ipfs-force-community/venus-messager/types"
)

const (
	gasEstimate = "gas estimate: "
	signMsg     = "sign msg: "
)

type MessageSelector struct {
	repo           repo.Repo
	log            *logrus.Logger
	cfg            *config.MessageServiceConfig
	nodeClient     *NodeClient
	addressService *AddressService
	walletService  *WalletService
	sps            *SharedParamsService
}

type msgErrInfo struct {
	id  string
	err string
}

func NewMessageSelector(repo repo.Repo,
	log *logrus.Logger,
	cfg *config.MessageServiceConfig,
	nodeClient *NodeClient,
	addressService *AddressService,
	walletService *WalletService,
	sps *SharedParamsService) *MessageSelector {
	return &MessageSelector{repo: repo,
		log:            log,
		cfg:            cfg,
		nodeClient:     nodeClient,
		addressService: addressService,
		walletService:  walletService,
		sps:            sps,
	}
}

func (messageSelector *MessageSelector) SelectMessage(ctx context.Context, ts *venusTypes.TipSet) ([]*types.Message,
	[]*types.Message, []*venusTypes.SignedMessage, []*types.Address, []msgErrInfo, error) {
	addrList, err := messageSelector.addressService.ListAddress(ctx)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	//sort by addr weight
	sort.Slice(addrList, func(i, j int) bool {
		return addrList[i].Weight < addrList[j].Weight
	})

	var selectMsg []*types.Message
	var expireMsgs []*types.Message
	var toPushMessage []*venusTypes.SignedMessage
	var modifyAddrs []*types.Address
	var msgsErrInfo []msgErrInfo

	var lk sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
	wg.Add(len(addrList))
	for _, addr := range addrList {
		go func(addr *types.Address) {
			sem <- struct{}{}
			defer func() {
				wg.Done()
				<-sem
			}()

			addrSelectMsg, addrExpireMsgs, addrToPushMessage, msgsErr, err := messageSelector.selectAddrMessage(ctx, addr, ts)
			if err != nil {
				messageSelector.log.Errorf("select message of %s fail %v", addr.Addr, err)
				return
			}
			lk.Lock()
			defer lk.Unlock()

			expireMsgs = append(expireMsgs, addrExpireMsgs...)
			toPushMessage = append(toPushMessage, addrToPushMessage...)
			if len(addrSelectMsg) > 0 {
				selectMsg = append(selectMsg, addrSelectMsg...)
				modifyAddrs = append(modifyAddrs, addr)
			}
			msgsErrInfo = append(msgsErrInfo, msgsErr...)
		}(addr)
	}

	wg.Wait()

	return selectMsg, expireMsgs, toPushMessage, modifyAddrs, msgsErrInfo, nil
}

func (messageSelector *MessageSelector) selectAddrMessage(ctx context.Context, addr *types.Address, ts *venusTypes.TipSet) ([]*types.Message, []*types.Message, []*venusTypes.SignedMessage, []msgErrInfo, error) {
	var toPushMessage []*venusTypes.SignedMessage

	addrsInfo, exit := messageSelector.walletService.GetAddressesInfo(addr.Addr)
	if !exit {
		return nil, nil, nil, nil, xerrors.Errorf("no wallet client")
	}

	var maxAllowPendingMessage uint64
	if messageSelector.sps.GetParams().SharedParams != nil {
		maxAllowPendingMessage = messageSelector.sps.GetParams().SelMsgNum
	}
	for _, addrInfo := range addrsInfo {
		if addrInfo.SelectMsgNum != 0 {
			maxAllowPendingMessage = addrInfo.SelectMsgNum
			break
		}
	}

	//判断是否需要推送消息
	actor, err := messageSelector.nodeClient.StateGetActor(ctx, addr.Addr, ts.Key())
	if err != nil {
		return nil, nil, nil, nil, xerrors.Errorf("actor of address %s not found", addr.Addr)
	}

	if actor.Nonce > addr.Nonce {
		messageSelector.log.Warnf("%s nonce in db %d is smaller than nonce on chain %d, update to latest", addr.Addr, addr.Nonce, actor.Nonce)
		addr.Nonce = actor.Nonce
		addr.UpdatedAt = time.Now()
		err := messageSelector.repo.AddressRepo().SaveAddress(ctx, addr)
		if err != nil {
			return nil, nil, nil, nil, xerrors.Errorf("update address %s nonce fail", addr.Addr)
		}
	}
	//todo push signed but not onchain message, when to resend message
	filledMessage, err := messageSelector.repo.MessageRepo().ListFilledMessageByAddress(addr.Addr)
	if err != nil {
		messageSelector.log.Warnf("list filled message %v", err)
	}
	for _, msg := range filledMessage {
		if actor.Nonce > msg.Nonce {
			continue
		}
		toPushMessage = append(toPushMessage, &venusTypes.SignedMessage{
			Message:   msg.UnsignedMessage,
			Signature: *msg.Signature,
		})
	}

	//消息排序
	messages, err := messageSelector.repo.MessageRepo().ListUnChainMessageByAddress(addr.Addr)
	if err != nil {
		return nil, nil, nil, nil, xerrors.Errorf("list %s unpackage message error %v", addr.Addr, err)
	}
	messages, expireMsgs := messageSelector.excludeExpire(ts, messages)
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Meta.ExpireEpoch < messages[j].Meta.ExpireEpoch
	})

	//sign new message
	nonceGap := addr.Nonce - actor.Nonce
	if nonceGap > maxAllowPendingMessage {
		messageSelector.log.Infof("%s there are %d message not to be package ", addr.Addr, nonceGap)
		return nil, expireMsgs, toPushMessage, nil, nil
	}
	selectCount := maxAllowPendingMessage - nonceGap

	//todo 如何筛选
	if len(messages) == 0 {
		messageSelector.log.Infof("%s have no message", addr.Addr)
		return nil, expireMsgs, toPushMessage, nil, nil
	}

	var count = uint64(0)
	var selectMsg []*types.Message
	var failedCount uint64
	var allowFailedNum uint64
	var msgsErrInfo []msgErrInfo
	if messageSelector.sps.GetParams().SharedParams != nil {
		allowFailedNum = messageSelector.sps.GetParams().MaxEstFailNumOfMsg
	}
	for _, msg := range messages {
		if count >= selectCount {
			break
		}
		if failedCount >= allowFailedNum {
			messageSelector.log.Warnf("the maximum number of failures has been reached %d", allowFailedNum)
			break
		}
		addrInfo, ok := messageSelector.walletService.GetAddressInfo(msg.WalletName, msg.From)
		if !ok {
			messageSelector.log.Warnf("not found wallet client %s", msg.WalletName)
			continue
		}
		if addrInfo.State != types.Alive && addrInfo.State != types.Forbiden {
			messageSelector.log.Infof("wallet %s address %v state is %s, skip select unchain message", msg.WalletName, addr.Addr, types.StateToString(addrInfo.State))
			continue
		}

		//分配nonce
		msg.Nonce = addr.Nonce

		// global msg meta
		newMsgMeta := messageSelector.messageMeta(msg.Meta)

		//todo 估算gas, spec怎么做？
		//通过配置影响 maxfee
		newMsg, err := messageSelector.GasEstimateMessageGas(ctx, msg.VMMessage(), newMsgMeta, ts.Key())
		if err != nil {
			failedCount++
			msgsErrInfo = append(msgsErrInfo, msgErrInfo{id: msg.ID, err: gasEstimate + err.Error()})
			if strings.Contains(err.Error(), "exit SysErrSenderStateInvalid(2)") {
				// SysErrSenderStateInvalid(2))
				messageSelector.log.Errorf("message %s estimate message fail %v break address %s", msg.ID, err, addr.Addr)
				break
			}
			messageSelector.log.Errorf("message %s estimate message fail %v, try to next message", msg.ID, err)

			continue
		}
		msg.GasFeeCap = newMsg.GasFeeCap
		msg.GasPremium = newMsg.GasPremium
		msg.GasLimit = newMsg.GasLimit

		unsignedCid := msg.UnsignedMessage.Cid()
		msg.UnsignedCid = &unsignedCid
		//签名
		data, err := msg.UnsignedMessage.ToStorageBlock()
		if err != nil {
			messageSelector.log.Errorf("calc message unsigned message id %s fail %v", msg.ID, err)
			continue
		}
		sig, err := addrInfo.WalletClient.WalletSign(ctx, addr.Addr, unsignedCid.Bytes(), core.MsgMeta{
			Type:  core.MTChainMsg,
			Extra: data.RawData(),
		})
		if err != nil {
			//todo client net crash?
			msgsErrInfo = append(msgsErrInfo, msgErrInfo{id: msg.ID, err: signMsg + err.Error()})
			messageSelector.log.Errorf("wallet sign failed %s fail %v", msg.ID, err)
			continue
		}

		msg.Signature = sig
		//state
		msg.State = types.FillMsg

		signedMsg := venusTypes.SignedMessage{
			Message:   msg.UnsignedMessage,
			Signature: *msg.Signature,
		}

		signedCid := signedMsg.Cid()
		msg.SignedCid = &signedCid

		selectMsg = append(selectMsg, msg)
		addr.Nonce++
		count++
	}

	messageSelector.log.Infof("address %s select message %d max nonce %d", addr.Addr, len(selectMsg), addr.Nonce)
	return selectMsg, expireMsgs, toPushMessage, msgsErrInfo, nil
}

func (messageSelector *MessageSelector) excludeExpire(ts *venusTypes.TipSet, msgs []*types.Message) ([]*types.Message, []*types.Message) {
	//todo check whether message is expired
	var result []*types.Message
	var expireMsg []*types.Message
	for _, msg := range msgs {
		if msg.Meta.ExpireEpoch != 0 && msg.Meta.ExpireEpoch <= ts.Height() {
			//expire
			msg.State = types.FailedMsg
			expireMsg = append(expireMsg, msg)
			continue
		}
		result = append(result, msg)
	}
	return result, expireMsg
}

func (messageSelector *MessageSelector) messageMeta(meta *types.MsgMeta) *types.MsgMeta {
	newMsgMeta := &types.MsgMeta{}
	*newMsgMeta = *meta
	globalMeta := messageSelector.sps.GetParams().GetMsgMeta()
	if globalMeta == nil {
		return newMsgMeta
	}

	if meta.GasOverEstimation == 0 {
		newMsgMeta.GasOverEstimation = globalMeta.GasOverEstimation
	}
	if meta.MaxFee.NilOrZero() {
		newMsgMeta.MaxFee = globalMeta.MaxFee
	}
	if meta.MaxFeeCap.NilOrZero() {
		newMsgMeta.MaxFeeCap = globalMeta.MaxFeeCap
	}

	return newMsgMeta
}

func (messageSelector *MessageSelector) GasEstimateMessageGas(ctx context.Context, msg *venusTypes.UnsignedMessage, meta *types.MsgMeta, tsk venusTypes.TipSetKey) (*venusTypes.UnsignedMessage, error) {
	if msg.GasLimit == 0 {
		gasLimit, err := messageSelector.nodeClient.GasEstimateGasLimit(ctx, msg, tsk)
		if err != nil {
			return nil, xerrors.Errorf("estimating gas used: %w", err)
		}
		//GasOverEstimation default value should be 1.25
		msg.GasLimit = int64(float64(gasLimit) * meta.GasOverEstimation)
	}

	if msg.GasPremium == venusTypes.EmptyInt || venusTypes.BigCmp(msg.GasPremium, venusTypes.NewInt(0)) == 0 {
		gasPremium, err := messageSelector.nodeClient.GasEstimateGasPremium(ctx, 10, msg.From, msg.GasLimit, tsk)
		if err != nil {
			return nil, xerrors.Errorf("estimating gas price: %w", err)
		}
		msg.GasPremium = gasPremium
	}

	if msg.GasFeeCap == venusTypes.EmptyInt || venusTypes.BigCmp(msg.GasFeeCap, venusTypes.NewInt(0)) == 0 {
		feeCap, err := messageSelector.nodeClient.GasEstimateFeeCap(ctx, msg, 20, venusTypes.EmptyTSK)
		if err != nil {
			return nil, xerrors.Errorf("estimating fee cap: %w", err)
		}
		msg.GasFeeCap = feeCap
	}

	CapGasFee(msg, meta.MaxFee)

	return msg, nil
}

func CapGasFee(msg *venusTypes.UnsignedMessage, maxFee abi.TokenAmount) {
	if maxFee.NilOrZero() {
		return
	}

	gl := venusTypes.NewInt(uint64(msg.GasLimit))
	totalFee := venusTypes.BigMul(msg.GasFeeCap, gl)

	if totalFee.LessThanEqual(maxFee) {
		return
	}

	msg.GasFeeCap = big.Div(maxFee, gl)
	msg.GasPremium = big.Min(msg.GasFeeCap, msg.GasPremium) // cap premium at FeeCap
}
