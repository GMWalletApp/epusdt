## 前言
本教程基于`宝塔`操作面板演示`Epusdt`如何运行，如果你未使用`宝塔`面板和有更强的服务器运维基础相信你也能根据教程的一些操作找到灵感！     

## 准备工作
- 请安装并启动用于守护 EPUSDT 进程的 `Supervisord` 服务。
- 准备一个`telgeram`机器人🤖（用于操作你的钱包和接收交易通知），申请教程参考：[https://w37fhy.cn/2364.html](https://w37fhy.cn/2364.html)
- 准备一个收银台域名，并完成解析(例如：`upay.dujiaoka.com`)。用于支付请求发起和扫码页面

## 开始使用
### 一、新增网站
在`宝塔`面板网站里新增一个网站，选择纯静态站点，数据库选项选「不创建」。
将提前解析好的收银台域名绑定。     
![新增网站](img/add_web.png)

### 二、数据库说明
EPUSDT 启动时会自动创建 SQLite 文件和表。请给运行 EPUSDT 的用户授予数据目录写入权限，并将数据库放在网站公开目录之外。

### 三、配置Epusdt
1.将编译好的`Epusdt`项目压缩包上传至刚刚新增的网站目录，随后解压。    
![网站目录](img/web_tree.png)
2.将`.env.example`重命名为`.env`文件，这个是配置文件   
3.编辑`.env`文件，填入你的配置     
```dotenv
app_name=epusdt
#下面配置你的域名，收银台会需要
app_uri=https://upay.dujiaoka.com
#是否开启debug，默认false
app_debug=false
#http服务监听端口
http_listen=:8000

#静态资源文件目录
static_path=/static
#缓存路径
runtime_root_path=/www/epusdt-data/runtime

#日志配置
log_save_path=/logs
log_max_size=32
log_max_age=7
max_backups=3

# SQLite 数据文件
db_type=sqlite
sqlite_database_filename=/www/epusdt-data/epusdt.db
runtime_sqlite_filename=epusdt-runtime.db

# 消息队列配置
queue_concurrency=10
queue_level_critical=6
queue_level_default=3
queue_level_low=1

#机器人Apitoken
tg_bot_token=
#telegram代理url(大陆地区服务器可使用一台国外服务器做反代tg的url)，如果运行的本来就是境外服务器，则无需填写
tg_proxy=
#管理员userid
tg_manage=

#api接口认证token(用于发起交易的签名认证，请勿外泄)
api_auth_token=

#订单过期时间(单位分钟)
order_expiration_time=10

#汇率相关配置请在后台“系统配置 -> 汇率配置”中设置
```
4. ⚠️注意：配置文件里面不认识的不要修改，留空即可，不会改又要瞎改，除非你对项目源代码很熟悉很有信心😁

### 四、配置反向代理
由于`Epusdt`会自动开启`http`服务并监听配置端口，所以可以直接用ip访问，例如: `http://127.0.0.1:8000`。     
这会显得非常不美观，还没有https。     
所以我们需要借助nginx的`反向代理`来为`Epusdt`绑定一个域名        
点击`宝塔`->`网站`->点击刚刚新增网站的`设置`->反向代理设置    
![反向代理](img/fanxiang.png)

### 五、赋予Epusdt执行权限
`linux`服务器需要赋予`Epust`执行权限方可启动。      
1.通过`ssh`进入服务器终端      
2.进入`epusdt`可执行文件所在目录（目录位置可以参考宝塔面板的网站根目录）       
3.执行命令```chmod +x epusdt```赋予权限(根据你的实际目录来，不用一比一抄我的，每个人的目录都不一样)     
![执行权限](img/chmodx.png)


### 六、配置supervisor
为了保证`Epusdt`常驻后台运行，我们需要配置`supervisor`来实现进程监听        
打开`宝塔supervisor`，添加守护进程     
![supervisor](img/supervisor.png)
启动命令为：epusdt所在目录+epusdt执行文件+空格+http+空格+start(例如：`/www/wwwroot/upay/epusdt-dev/epusdt http start`)       
出现如下图则代表成功：     
![supervisor成功](img/supervisor_success.png)

### 七、测试
以上步骤完成无误后，进入`telegram`机器人🤖，如果机器人有反馈则代表运行正常！     
![telegram机器人](img/epusdt_bot.png)


## 其他注意事项
- 1.所有`.env`配置文件有了修改后都需要重启supervisor进程
- 2.教程所示的目录均为参考，请勿1:1照抄，根据自己实际情况来
- 3.`windows`与`linux`不同，`windows`请直接运行`epusdt.exe`即可，无需赋予执行权限
