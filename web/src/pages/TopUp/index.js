import React, { useEffect, useState } from 'react';
import { API, isMobile, showError, showInfo, showSuccess } from '../../helpers';
import {
  renderNumber,
  renderQuota,
  renderQuotaWithAmount,
} from '../../helpers/render';
import {
  Col,
  Layout,
  Row,
  Typography,
  Card,
  Button,
  Form,
  Divider,
  Space,
  Modal,
  Toast,
} from '@douyinfe/semi-ui';
import Title from '@douyinfe/semi-ui/lib/es/typography/title';
import Text from '@douyinfe/semi-ui/lib/es/typography/text';
import { Link } from 'react-router-dom';
import {QRCode} from "antd";
import Countdown from "antd/es/statistic/Countdown";

const TopUp = () => {
  const [redemptionCode, setRedemptionCode] = useState('');
  const [topUpCode, setTopUpCode] = useState('');
  const [topUpCount, setTopUpCount] = useState(0);
  const [minTopupCount, setMinTopUpCount] = useState(1);
  const [amount, setAmount] = useState(0.0);
  const [minTopUp, setMinTopUp] = useState(1);
  const [topUpLink, setTopUpLink] = useState('');
  const [enableOnlineTopUp, setEnableOnlineTopUp] = useState(false);
  const [userQuota, setUserQuota] = useState(0);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [open, setOpen] = useState(false);
  const [payWay, setPayWay] = useState('');
  const [payType, setPayType] = useState(''); // easy wx;

  const [wxpayOpen, setWxpayOpen] = useState(false);
  const [status, setStatus] = useState("loading");
  const [timeId, setTimeId] = useState(0);
  const [qrCodeUrl, setQrCodeUrl] = useState("https://ai.annyun.cn");
  const [deadline, setDeadLine] = useState(0);

  const topUp = async () => {
    if (redemptionCode === '') {
      showInfo('请输入兑换码！');
      return;
    }
    setIsSubmitting(true);
    try {
      const res = await API.post('/api/user/topup', {
        key: redemptionCode,
      });
      const { success, message, data } = res.data;
      if (success) {
        showSuccess('兑换成功！');
        Modal.success({
          title: '兑换成功！',
          content: '成功兑换额度：' + renderQuota(data),
          centered: true,
        });
        setUserQuota((quota) => {
          return quota + data;
        });
        setRedemptionCode('');
      } else {
        showError(message);
      }
    } catch (err) {
      showError('请求失败');
    } finally {
      setIsSubmitting(false);
    }
  };

  const openTopUpLink = () => {
    if (!topUpLink) {
      showError('超级管理员未设置充值链接！');
      return;
    }
    window.open(topUpLink, '_blank');
  };

  const preTopUp = async (payment) => {
    if (!enableOnlineTopUp) {
      showError('管理员未开启在线充值！');
      return;
    }
    await getAmount();
    if (topUpCount < minTopUp) {
      showError('充值数量不能小于' + minTopUp);
      return;
    }
    setPayWay(payment);
    setOpen(true);
  };

  const onlineTopUp = async () => {
    if (amount === 0) {
      await getAmount();
    }
    if (topUpCount < minTopUp) {
      showError('充值数量不能小于' + minTopUp);
      return;
    }
    setOpen(false);
    try {
      let url = '/api/user/pay';
      if (payType == "wx") {
        url = '/api/user/wxpay';
      }
      const res = await API.post(url, {
        amount: parseInt(topUpCount),
        top_up_code: topUpCode,
        payment_method: payWay,
      });
      if (res !== undefined) {
        const { message, data } = res.data;
        // showInfo(message);
        if (message === 'success') {
          if (payType == "easy") {
            let params = data;
            let url = res.data.url;
            let form = document.createElement('form');
            form.action = url;
            form.method = 'POST';
            // 判断是否为safari浏览器
            let isSafari =
              navigator.userAgent.indexOf('Safari') > -1 &&
              navigator.userAgent.indexOf('Chrome') < 1;
            if (!isSafari) {
              form.target = '_blank';
            }
            for (let key in params) {
              let input = document.createElement('input');
              input.type = 'hidden';
              input.name = key;
              input.value = params[key];
              form.appendChild(input);
            }
            document.body.appendChild(form);
            form.submit();
            document.body.removeChild(form);
          }
          else {
            setWxpayOpen(true);
            setQrCodeUrl(res.data.url);
            setDeadLine(Date.now() + 5 * 60 * 1000);
            setStatus("active");
            // 定时查询订单状态。
            const startTime = new Date().getTime();
            let intervalId= setInterval(
              () => checkOrder(startTime, intervalId, res.data.data),
              6000,
            );
            setTimeId(intervalId);
          }

        } else {
          showError(data);
          // setTopUpCount(parseInt(res.data.count));
          // setAmount(parseInt(data));
        }
      } else {
        showError(res);
      }
    } catch (err) {
      console.log(err);
    } finally {
    }
  };

  const checkOrder = async (startTime, intervalId, orderId) => {
    if (new Date().getTime() > startTime + 5 * 60 * 1000) {
      window.clearInterval(intervalId);
      setWxpayOpen(false);
    } else {
      const res = await API.get('/api/user/checkOrder?orderId=' + orderId);
      if (!res) {
        showError("查询订单状态失败");
      } else if (res.data == "success") {
        showSuccess("支付成功！");
        setTimeout(() => {
          window.clearInterval(intervalId);

        }, 3000);
      }
    }
  };

  const onFinish = () => {
    window.clearInterval(timeId);
    setWxpayOpen(false)
  };

  const getUserQuota = async () => {
    let res = await API.get(`/api/user/self`);
    const { success, message, data } = res.data;
    if (success) {
      setUserQuota(data.quota);
    } else {
      showError(message);
    }
  };

  useEffect(() => {
    let status = localStorage.getItem('status');
    if (status) {
      status = JSON.parse(status);
      if (status.top_up_link) {
        setTopUpLink(status.top_up_link);
      }
      if (status.min_topup) {
        setMinTopUp(status.min_topup);
      }
      if (status.enable_online_topup) {
        setEnableOnlineTopUp(status.enable_online_topup);
      }
      setPayType(status.pay_type);
    }
    getUserQuota().then();
  }, []);

  const renderAmount = () => {
    // console.log(amount);
    return amount + '元';
  };

  const getAmount = async (value) => {
    if (value === undefined) {
      value = topUpCount;
    }
    try {
      const res = await API.post('/api/user/amount', {
        amount: parseFloat(value),
        top_up_code: topUpCode,
      });
      if (res !== undefined) {
        const { message, data } = res.data;
        // showInfo(message);
        if (message === 'success') {
          setAmount(parseFloat(data));
        } else {
          setAmount(0);
          Toast.error({ content: '错误：' + data, id: 'getAmount' });
          // setTopUpCount(parseInt(res.data.count));
          // setAmount(parseInt(data));
        }
      } else {
        showError(res);
      }
    } catch (err) {
      console.log(err);
    } finally {
    }
  };

  const handleCancel = () => {
    setOpen(false);
  };

  return (
    <div>
      <Layout>
        <Layout.Header>
          <h3>我的钱包</h3>
        </Layout.Header>
        <Layout.Content>
          <Modal
            title='确定要充值吗'
            visible={open}
            onOk={onlineTopUp}
            onCancel={handleCancel}
            maskClosable={false}
            size={'small'}
            centered={true}
          >
            <p>充值数量：{topUpCount}</p>
            <p>实付金额：{renderAmount()}</p>
            <p>是否确认充值？</p>
          </Modal>
          <Modal
            title='付款'
            visible={wxpayOpen}
            onCancel={() => setWxpayOpen(false)}
            maskClosable={false}
            size={'small'}
            centered={true}
          >
            <div style={{maxWidth: '100%', textAlign: 'center', display: 'flex', flexDirection: 'column', alignItems: 'center'}}>
              {status == "active" && (
                <Countdown
                  title='订单创建成功，请扫码支付'
                  value={deadline}
                  onFinish={onFinish}
                />
              )}
              <QRCode
                value={qrCodeUrl}
                size={200}
                status={status}
                bgColor={"white"}
              />
            </div>

          </Modal>
          <div
            style={{ marginTop: 20, display: 'flex', justifyContent: 'center' }}
          >
            <Card style={{ width: '500px', padding: '20px' }}>
              <Title level={3} style={{ textAlign: 'center' }}>
                余额 {renderQuota(userQuota)}
              </Title>
              <div style={{ marginTop: 20 }}>
                <Divider>兑换余额</Divider>
                <Form>
                  <Form.Input
                    field={'redemptionCode'}
                    label={'兑换码'}
                    placeholder='兑换码'
                    name='redemptionCode'
                    value={redemptionCode}
                    onChange={(value) => {
                      setRedemptionCode(value);
                    }}
                  />
                  <Space>
                    {topUpLink ? (
                      <Button
                        type={'primary'}
                        theme={'solid'}
                        onClick={openTopUpLink}
                      >
                        获取兑换码
                      </Button>
                    ) : null}
                    <Button
                      type={'warning'}
                      theme={'solid'}
                      onClick={topUp}
                      disabled={isSubmitting}
                    >
                      {isSubmitting ? '兑换中...' : '兑换'}
                    </Button>
                  </Space>
                </Form>
              </div>
              <div style={{ marginTop: 20 }}>
                <Divider>在线充值</Divider>
                <Form>
                  <Form.Input
                    disabled={!enableOnlineTopUp}
                    field={'redemptionCount'}
                    label={'实付金额：' + renderAmount()}
                    placeholder={
                      '充值数量，最低 ' + renderQuotaWithAmount(minTopUp)
                    }
                    name='redemptionCount'
                    type={'number'}
                    value={topUpCount}
                    onChange={async (value) => {
                      if (value < 1) {
                        value = 1;
                      }
                      setTopUpCount(value);
                      await getAmount(value);
                    }}
                  />
                  <Space>
                    {payType == "easy" && (
                      <Button
                        type={'primary'}
                        theme={'solid'}
                        onClick={async () => {
                          preTopUp('zfb');
                        }}
                      >
                        支付宝
                      </Button>
                    )}
                    <Button
                      style={{
                        backgroundColor: 'rgba(var(--semi-green-5), 1)',
                      }}
                      type={'primary'}
                      theme={'solid'}
                      onClick={async () => {
                        preTopUp('wx');
                      }}
                    >
                      微信
                    </Button>
                  </Space>
                </Form>
              </div>
              {/*<div style={{ display: 'flex', justifyContent: 'right' }}>*/}
              {/*    <Text>*/}
              {/*        <Link onClick={*/}
              {/*            async () => {*/}
              {/*                window.location.href = '/topup/history'*/}
              {/*            }*/}
              {/*        }>充值记录</Link>*/}
              {/*    </Text>*/}
              {/*</div>*/}
            </Card>
          </div>
        </Layout.Content>
      </Layout>
    </div>
  );
};

export default TopUp;
