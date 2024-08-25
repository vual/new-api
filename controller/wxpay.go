package controller

import (
	"context"
	"crypto/x509"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
	"log"
	"one-api/common"
	"one-api/constant"
	"one-api/model"
	"one-api/service"
	"time"
)

// 支付公共uri
const (
	publicKeyUrl = "https://api.mch.weixin.qq.com/v3/certificates"
	nativePayUrl = "https://api.mch.weixin.qq.com/v3/pay/transactions/native"
)

func GetWxPayClient(ctx context.Context) *core.Client {
	if constant.WxPayMchId == "" || constant.WxPayApiV3Key == "" || constant.WxPaySerialNo == "" || constant.WxPayKeyPath == "" || constant.WxPayCertPath == "" {
		return nil
	}
	mchPrivateKey, err := utils.LoadPrivateKeyWithPath(constant.WxPayKeyPath)
	if err != nil {
		log.Fatal("load merchant private key error")
	}

	// 使用商户私钥等初始化 client，并使它具有自动定时获取微信支付平台证书的能力
	opts := []core.ClientOption{
		option.WithWechatPayAutoAuthCipher(constant.WxPayMchId, constant.WxPaySerialNo, mchPrivateKey, constant.WxPayApiV3Key),
	}
	client, err := core.NewClient(ctx, opts...)
	if err != nil {
		log.Fatalf("new wechat pay client err:%s", err)
		return nil
	}

	return client
}

func WxPayNative(c *gin.Context) {
	var req EpayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(200, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	if req.Amount < getMinTopup() {
		c.JSON(200, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", getMinTopup())})
		return
	}

	id := c.GetInt("id")
	group, err := model.CacheGetUserGroup(id)
	if err != nil {
		c.JSON(200, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney := getPayMoney(float64(req.Amount), group)
	if payMoney < 0.01 {
		c.JSON(200, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	callBackAddress := service.GetCallbackAddress()
	tradeNo := fmt.Sprintf("%s%d", common.GetRandomString(6), time.Now().Unix())
	tradeNo = fmt.Sprintf("USR%dNO%s", id, tradeNo)

	amount := req.Amount
	if !common.DisplayInCurrencyEnabled {
		amount = amount / int(common.QuotaPerUnit)
	}
	topUp := &model.TopUp{
		UserId:     id,
		Amount:     amount,
		Money:      payMoney,
		TradeNo:    tradeNo,
		CreateTime: time.Now().Unix(),
		Status:     "pending",
	}
	err = topUp.Insert()
	if err != nil {
		c.JSON(200, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}
	//log.Printf("orderId: %d", topUp.Id)

	ctx := context.Background()
	client := GetWxPayClient(ctx)
	if client == nil {
		c.JSON(200, gin.H{"message": "error", "data": "当前管理员未配置支付信息"})
		return
	}

	svc := native.NativeApiService{Client: client}
	// 发送请求
	resp, _, err := svc.Prepay(ctx,
		native.PrepayRequest{
			Appid:       core.String(constant.WxPayAppId),
			Mchid:       core.String(constant.WxPayMchId),
			Description: core.String(fmt.Sprintf("%d的支付订单", id)),
			OutTradeNo:  core.String(tradeNo),
			Attach:      core.String(fmt.Sprintf("%d的支付订单", id)),
			NotifyUrl:   core.String(callBackAddress + "/api/user/wxpay/notify"),
			Amount: &native.Amount{
				Total: core.Int64(int64(payMoney * 100)),
			},
		},
	)
	if err != nil {
		c.JSON(200, gin.H{"message": "error", "data": "创建微信支付订单支付失败！"})
		return
	}
	// 使用微信扫描 resp.code_url 对应的二维码，即可体验Native支付
	//log.Printf("status=%d resp=%s", result.Response.StatusCode, resp)

	// 放入redis
	common.RedisSet("topup_order::"+tradeNo, topUp.Status, time.Duration(6)*time.Minute)

	c.JSON(200, gin.H{"message": "success", "data": tradeNo, "url": resp.CodeUrl})
}

func WxPayNotify(c *gin.Context) {

	wechatPayCert, err := utils.LoadCertificate(constant.WxPayCertPath)
	// 2. 使用本地管理的微信支付平台证书获取微信支付平台证书访问器
	certificateVisitor := core.NewCertificateMapWithList([]*x509.Certificate{wechatPayCert})
	// 3. 使用apiv3 key、证书访问器初始化 `notify.Handler`
	handler, err := notify.NewRSANotifyHandler(constant.WxPayApiV3Key, verifiers.NewSHA256WithRSAVerifier(certificateVisitor))

	transaction := new(payments.Transaction)
	notifyReq, err := handler.ParseNotifyRequest(context.Background(), c.Request, transaction)
	// 如果验签未通过，或者解密失败
	if err != nil {
		fmt.Println(err)
		return
	}
	// 处理通知内容
	fmt.Println(notifyReq.Summary)
	fmt.Println(transaction.TransactionId)
	// 在这里处理你的业务逻辑，如更新订单状态
	//log.Printf("Transaction ID: %s", transaction.TransactionId)
	tradeNo := *transaction.OutTradeNo
	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil {
		// 响应微信服务器
		c.JSON(200, gin.H{"code": "ERROR", "message": "找不到订单！"})
	}

	payAmout := *transaction.Amount.PayerTotal
	amount := int64(topUp.Amount * 100)
	if payAmout != amount {
		// 响应微信服务器
		c.JSON(200, gin.H{"code": "ERROR", "message": "支付金额错误！"})
	}

	if topUp.Status == "pending" {
		topUp.Status = "success"
		err := topUp.Update()
		if err != nil {
			log.Printf("微信支付回调更新订单失败: %v", topUp)
			return
		}
		//user, _ := model.GetUserById(topUp.UserId, false)
		//user.Quota += topUp.Amount * 500000
		err = model.IncreaseUserQuota(topUp.UserId, topUp.Amount*int(common.QuotaPerUnit))
		if err != nil {
			log.Printf("微信支付回调更新用户失败: %v", topUp)
			return
		}
		log.Printf("微信支付回调更新用户成功 %v", topUp)
		model.RecordLog(topUp.UserId, model.LogTypeTopup, fmt.Sprintf("使用在线充值成功，充值金额: %v，支付金额：%f", common.LogQuota(topUp.Amount*int(common.QuotaPerUnit)), topUp.Money))

		// 更新redis里的状态
		common.RedisSet("topup_order::"+tradeNo, topUp.Status, time.Duration(6)*time.Minute)
	}

	// 响应微信服务器
	c.JSON(200, gin.H{"code": "SUCCESS", "message": "OK"})
}

func WxPayCheckOrder(c *gin.Context) {
	orderId := c.Query("orderId")
	status, error := common.RedisGet("topup_order::" + orderId)
	if error != nil {
		c.JSON(200, gin.H{"message": "failed"})
	}
	c.JSON(200, gin.H{"message": "success", "data": status})
}
