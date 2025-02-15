package controller

var authMap = map[string]string{
	"PushMessageWithId":        "write",
	"GetMessageByFromAndNonce": "read",
	"ListMessage":              "admin",
	"ListMessageByAddress":     "admin",
	"GetSharedParams":          "admin",
	"PushMessage":              "write",
	"GetMessageByUid":          "read",
	"UpdateMessageStateByID":   "admin",
	"UpdateAllFilledMessage":   "admin",
	"GetWalletByName":          "admin",
	"UpdateWallet":             "admin",
	"ListAddress":              "admin",
	"HasMessageByUid":          "read",
	"GetMessageBySignedCid":    "read",
	"SetSharedParams":          "admin",
	"SetSelectMsgNum":          "admin",
	"WaitMessage":              "read",
	"ListWallet":               "admin",
	"ListRemoteWalletAddress":  "admin",
	"GetAddress":               "admin",
	"ListNode":                 "admin",
	"MarkBadMessage":           "admin",
	"DeleteWallet":             "admin",
	"SaveAddress":              "admin",
	"HasAddress":               "admin",
	"UpdateNonce":              "admin",
	"DeleteNode":               "admin",
	"ReplaceMessage":           "admin",
	"SaveWallet":               "admin",
	"HasWallet":                "admin",
	"DeleteAddress":            "admin",
	"SaveNode":                 "admin",
	"GetNode":                  "admin",
	"HasWalletAddress":         "read",
	"UpdateMessageStateByCid":  "admin",
	"UpdateFilledMessageByID":  "admin",
	"GetWalletByID":            "admin",
	"RefreshSharedParams":      "admin",
	"ActiveAddress":            "admin",
	"ListWalletAddress":        "admin",
	"GetMessageByUnsignedCid":  "read",
	"RepublishMessage":         "admin",
	"HasNode":                  "admin",
	"GetWalletAddress":         "admin",
	"ForbiddenAddress":         "admin",
	"GetMessageByCid":          "read",
	"ListFailedMessage":        "admin",
	"ListBlockedMessage":       "admin",
}
