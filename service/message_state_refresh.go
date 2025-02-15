package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/filecoin-project/venus-messager/utils"

	"github.com/filecoin-project/go-state-types/abi"
	venustypes "github.com/filecoin-project/venus/pkg/types"
	"github.com/ipfs/go-cid"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/venus-messager/models/repo"
	"github.com/filecoin-project/venus-messager/types"
)

func (ms *MessageService) refreshMessageState(ctx context.Context) {
	go func() {
		for {
			select {
			case h := <-ms.headChans:
				ms.log.Info("start refresh message state")
				now := time.Now()
				if err := ms.doRefreshMessageState(ctx, h); err != nil {
					ms.log.Errorf("doRefreshMessageState occurs unexpected err:\n%v\n", err)
				}
				ms.log.Infof("end refresh message state, cost %d 'ms' ", time.Since(now).Milliseconds())
			case <-ctx.Done():
				ms.log.Warnf("context error: %v", ctx.Err())
				return
			}
		}
	}()
}

func (ms *MessageService) doRefreshMessageState(ctx context.Context, h *headChan) error {
	if len(h.apply) == 0 {
		return nil
	}

	revertMsgs, err := ms.processRevertHead(ctx, h)
	if err != nil {
		return err
	}

	applyMsgs, err := ms.processBlockParentMessages(ctx, h.apply)
	if err != nil {
		return xerrors.Errorf("process apply failed %v", err)
	}

	var tsList tipsetList
	tsKeys := make(map[abi.ChainEpoch]venustypes.TipSetKey)
	for _, ts := range h.apply {
		height := ts.Height()
		tsList = append(tsList, &tipsetFormat{Key: ts.Key().String(), Height: int64(height)})
		tsKeys[height] = ts.Key()
	}

	// update db
	replaceMsg := make(map[string]*types.Message)
	err = ms.repo.Transaction(func(txRepo repo.TxRepo) error {
		for _, msg := range applyMsgs {
			localMsg, err := txRepo.MessageRepo().GetMessageByFromAndNonce(msg.msg.From, msg.msg.Nonce)
			if err != nil {
				ms.log.Warnf("msg not exit in local db maybe address %s send out of messager", msg.msg.From)
				continue
			}

			if localMsg.UnsignedCid == nil || *localMsg.UnsignedCid != msg.cid {
				//replace msg
				unsignedCid := msg.msg.Cid()
				localMsg.UnsignedMessage = *msg.msg
				localMsg.UnsignedCid = &unsignedCid
				localMsg.SignedCid = &msg.cid
				localMsg.State = types.ReplacedMsg
				localMsg.Receipt = msg.receipt
				localMsg.Height = int64(msg.height)
				localMsg.TipSetKey = tsKeys[msg.height]
				if err = txRepo.MessageRepo().SaveMessage(localMsg); err != nil {
					return xerrors.Errorf("update message receipt failed, cid:%s failed:%v", msg.cid.String(), err)
				}
				replaceMsg[localMsg.ID] = localMsg
				ms.log.Warnf("replace message old msg cid %s new msg cid %s", localMsg.UnsignedCid, msg.cid)
			} else {
				if err = txRepo.MessageRepo().UpdateMessageInfoByCid(msg.cid.String(), msg.receipt, msg.height, types.OnChainMsg, tsKeys[msg.height]); err != nil {
					return xerrors.Errorf("update message receipt failed, cid:%s failed:%v", msg.cid.String(), err)
				}
			}
			delete(revertMsgs, msg.cid)
		}
		for cid := range revertMsgs {
			if err := txRepo.MessageRepo().UpdateMessageInfoByCid(cid.String(), &venustypes.MessageReceipt{ExitCode: -1},
				abi.ChainEpoch(0), types.FillMsg, venustypes.EmptyTSK); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// update cache
	for id, msg := range replaceMsg {
		ms.messageState.SetMessage(id, msg)
	}

	for _, msg := range applyMsgs {
		if err := ms.messageState.UpdateMessageByCid(msg.cid, func(message *types.Message) error {
			message.Receipt = msg.receipt
			message.Height = int64(msg.height)
			message.State = types.OnChainMsg
			return nil
		}); err != nil {
			ms.log.Errorf("update message failed cid: %s error: %v", msg.cid.String(), err)
		}
	}

	for cid := range revertMsgs {
		if err := ms.messageState.UpdateMessageByCid(cid, func(message *types.Message) error {
			message.Receipt = &venustypes.MessageReceipt{ExitCode: -1}
			message.Height = 0
			message.State = types.FillMsg
			return nil
		}); err != nil {
			ms.log.Errorf("update message failed cid: %s error: %v", cid.String(), err)
		}
	}

	ms.tsCache.CurrHeight = int64(h.apply[0].Height())
	ms.tsCache.AddTs(tsList...)
	if err := ms.storeTipset(); err != nil {
		ms.log.Errorf("store tipsetkey failed %v", err)
	}

	ms.log.Infof("process block %d, revert %d message apply %d message ", ms.tsCache.CurrHeight, len(revertMsgs), len(applyMsgs))
	ms.triggerPush <- h.apply[0]

	return nil
}

func (ms *MessageService) processRevertHead(ctx context.Context, h *headChan) (map[cid.Cid]struct{}, error) {
	revertMsgs := make(map[cid.Cid]struct{})
	for _, ts := range h.revert {
		msgs, err := ms.repo.MessageRepo().ListFilledMessageByHeight(ts.Height())
		if err != nil {
			return nil, xerrors.Errorf("found message at height %d error %v", ts.Height(), err)
		}

		addrs := ms.walletService.AllAddresses()
		for _, msg := range msgs {
			if _, ok := addrs[msg.From]; ok && msg.UnsignedCid != nil {
				revertMsgs[*msg.UnsignedCid] = struct{}{}
			}

		}
	}

	return revertMsgs, nil
}

type pendingMessage struct {
	cid     cid.Cid
	msg     *venustypes.UnsignedMessage
	height  abi.ChainEpoch
	receipt *venustypes.MessageReceipt
}

func (ms *MessageService) processBlockParentMessages(ctx context.Context, apply []*venustypes.TipSet) ([]pendingMessage, error) {
	var applyMsgs []pendingMessage
	addrs := ms.walletService.AllAddresses()
	for _, ts := range apply {
		bcid := ts.At(0).Cid()
		height := ts.Height()
		msgs, err := ms.nodeClient.ChainGetParentMessages(ctx, bcid)
		if err != nil {
			return nil, xerrors.Errorf("get parent message failed %w", err)
		}

		receipts, err := ms.nodeClient.ChainGetParentReceipts(ctx, bcid)
		if err != nil {
			return nil, xerrors.Errorf("get parent Receipt failed %w", err)
		}

		if len(msgs) != len(receipts) {
			return nil, xerrors.Errorf("messages not match receipts, %d != %d", len(msgs), len(receipts))
		}

		for i := range receipts {
			msg := msgs[i].Message
			if _, ok := addrs[msg.From]; ok {
				applyMsgs = append(applyMsgs, pendingMessage{
					height:  height,
					receipt: receipts[i],
					msg:     msg,
					cid:     msg.Cid(),
				})
			}
		}
	}
	return applyMsgs, nil
}

type tipsetFormat struct {
	Key    string
	Height int64
}

func (ms *MessageService) storeTipset() error {
	if len(ms.tsCache.Cache) > maxStoreTipsetCount {
		ms.tsCache.ReduceTs()
	}

	return updateTipsetFile(ms.cfg.TipsetFilePath, ms.tsCache)
}

type tipsetList []*tipsetFormat

func (tl tipsetList) Len() int {
	return len(tl)
}

func (tl tipsetList) Swap(i, j int) {
	tl[i], tl[j] = tl[j], tl[i]
}

func (tl tipsetList) Less(i, j int) bool {
	return tl[i].Height > tl[j].Height
}

func readTipsetFile(filePath string) (*TipsetCache, error) {
	b, err := utils.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	if len(b) < 3 { // skip empty content
		return &TipsetCache{
			Cache:      map[int64]*tipsetFormat{},
			CurrHeight: 0,
		}, nil
	}
	var tsCache TipsetCache
	if err := json.Unmarshal(b, &tsCache); err != nil {
		return nil, err
	}

	return &tsCache, nil
}

// original data will be cleared
func updateTipsetFile(filePath string, tsCache *TipsetCache) error {
	return utils.WriteFile(filePath, tsCache)
}
