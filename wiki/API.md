# 前言
开发者可通过`Epusdt`提供的`http api`将交易功能集成至任何系统

# 接口统一加密方式
### 签名算法MD5
签名生成的通用步骤如下：            

第一步，将所有非空参数值的参数按照参数名ASCII码从小到大排序（字典序），使用URL键值对的格式（即key1=value1&key2=value2…）拼接成`待加密参数`。              

重要规则：   
◆ 参数名ASCII码从小到大排序（字典序）；         
◆ 如果参数的值为空不参与签名；        
◆ 参数名区分大小写；
第二步，`待加密参数`最后拼接上`api接口认证token`得到`待签名字符串`，并对`待签名字符串`进行MD5运算，再将得到的`MD5字符串`所有字符转换为`小写`，得到签名`signature`。 注意：`signature`的长度为32个字节。

举例：

假设传送的参数如下：      
```
order_id : 20220201030210321
amount : 42
notify_url : http://example.com/notify
redirect_url : http://example.com/redirect
```

假设api接口认证token为：`epusdt_password_xasddawqe`(api接口认证token可以在`.env`文件设置)         

第一步：对参数按照key=value的格式，并按照参数名ASCII字典序排序如下：       
```
amount=42&notify_url=http://example.com/notify&order_id=20220201030210321&redirect_url=http://example.com/redirect
```
第二步：拼接API密钥并加密：
```
MD5(amount=42&notify_url=http://example.com/notify&order_id=20220201030210321&redirect_url=http://example.com/redirectepusdt_password_xasddawqe)
```

最终得到最终发送的数据：    
```
order_id : 20220201030210321
amount : 42
notify_url : http://example.com/notify
redirect_url : http://example.com/redirect
signature : 1cd4b52df5587cfb1968b0c0c6e156cd
```

### PHP加密示例
```php
    function epusdtSign(array $parameter, string $signKey)
    {
        ksort($parameter);
        reset($parameter); 
        $sign = '';
        $urls = '';
        foreach ($parameter as $key => $val) {
            if ($val == '') continue;
            if ($key != 'signature') {
                if ($sign != '') {
                    $sign .= "&";
                    $urls .= "&";
                }
                $sign .= "$key=$val"; 
                $urls .= "$key=" . urlencode($val); 
            }
        }
        $sign = md5($sign . $signKey);//密码追加进入开始MD5签名
        return $sign;
    }
```

# 创建交易接口

## POST 创建交易

POST /payments/gmpay/v1/order/create-transaction

> Body 请求参数

```json
{
  "order_id": "2022123321312321321",
  "amount": 100,
  "notify_url": "http://example.com/",
  "redirect_url": "http://example.com/",
  "signature": "xsadaxsaxsa"
}
```

### 请求参数

|名称|位置|类型|必选| 中文名       | 说明            |
|---|---|---|---|-----------|---------------|
|body|body|object| 否 ||           |
|» order_id|body|string| 是 | 请求支付订单号   |           |
|» amount|body|number| 是 | 支付金额 | 小数点保留后2位，且最少需能兑换 0.01 USDT；若使用 USD，则最少 0.01 |
|» notify_url|body|string| 是 | 异步回调地址    |           |
|» redirect_url|body|string| 否 | 同步跳转地址    ||
|» signature|body|string| 是 | 签名        | 接口统一加密方式              |

> 返回示例

> 成功

```json
{
  "status_code": 200,
  "message": "success",
  "data": {
    "trade_id": "202203271648380592218340",
    "order_id": "9",
    "amount": 53,
    "actual_amount": 7.9104,
    "token": "TNEns8t9jbWENbStkQdVQtHMGpbsYsQjZK",
    "expiration_time": 1648381192,
    "payment_url": "http://example.com/pay/checkout-counter/202203271648380592218340"
  },
  "request_id": "b1344d70-ff19-4543-b601-37abfb3b3686"
}
```
### 返回结果

|状态码|状态码含义|说明|数据模型|
|---|---|---|---|
|200|[OK](https://tools.ietf.org/html/rfc7231#section-6.3.1)|成功|Inline|

### 返回数据结构

状态码 **200**

| 名称                 | 类型      | 解释        | 说明                            |
|--------------------|---------|-----------|-------------------------------|
| » status_code      | integer | 请求状态      | 请参考下方[status_code返回状态码及含义](#status_code返回状态码及含义) |
| » message          | string  | 消息        ||
| » data             | object  | 返回数据      ||
| »» trade_id        | string  | 交易号       ||
| »» order_id        | string  | 请求支付订单号   ||
| »» amount          | float | 请求支付金额    | CNY,保留2位小数                    |
| »» actual_amount   | float   | 实际需要支付的金额 | USDT,保留四位小数                   |
| »» token           | string  | 钱包地址      |                               |
| »» expiration_time | integer | 过期时间      | 时间戳秒                          |
| »» payment_url     | string  | 收银台地址     |                               |
| » request_id       | string  | true      |                               |


# 异步回调

支付成功后，`Epusdt`会向目标服务器发生异步通知，告知该笔交易已经支付完成。          
失败`Epusdt`最高最多重试5次，请注意验证消息签名。      
目标服务器处理完成后请返回字符串`ok`即可，否则`Epusdt`会一直重试发送消息，最高5次     

POST 【异步回调地址】

> Body 请求参数

```json
{
  "trade_id": "202203251648208648961728",
  "order_id": "2022123321312321321",
  "amount": 100,
  "actual_amount": 15.625,
  "token": "TNEns8t9jbWENbStkQdVQtHMGpbsYsQjZK",
  "block_transaction_id": "123333333321232132131",
  "signature": "xsadaxsaxsa",
  "status": 2
}
```

### 请求参数

|名称|位置| 类型     |必选| 中文名                 | 说明              |
|---|---|--------|---|---------------------|-----------------|
|body|body| object | 否 ||                     |
|» trade_id|body| string | 是 | 交易号                 |                 |
|» order_id|body| string | 是 | 请求支付订单号             |                 |
|» amount|body| float  | 是 | 支付金额(CNY)           | 小数点保留后2位 |
|» actual_amount|body| float  | 是 | 实际需要支付的usdt金额(USDT) | 小数点保留后4位 |
|» token|body| string | 是 | 钱包地址                | |
|» block_transaction_id|body| string | 是 | 区块交易号               |  |
|» signature|body| string | 是 | 签名                  |                 |
|» status|body| int    | 是 | 订单状态                | 1：等待支付，2：支付成功，3：已过期        | 

# status_code返回状态码及含义

| 状态码 | 说明  | 
|-----|-----|
|400|系统错误|
|401|签名认证错误|
|10002|支付交易已存在，请勿重复创建|
|10003|无可用钱包地址，无法发起支付|
|10004|支付金额有误, 无法满足最小支付单位|
|10005|无可用金额通道|
|10006|汇率计算错误|
|10007|订单区块已处理|
|10008|订单不存在|
|10009|无法解析参数|
