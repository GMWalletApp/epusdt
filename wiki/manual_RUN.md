## 写在前面

- 此教程专为有洁癖的宝宝们准备。不使用任何一键安装脚本。面板党可以退散了！！
- 本人测试环境是 Debian 11 其他的没测试。
## 1.下载源代码
```bash
cd /var/www/
mkdir epusdt
chmod 777 -R /var/www/epusdt
cd epusdt
# 从项目 Releases 下载适合本机架构的当前版本，解压到本目录。
```
[下载 EPUSDT](https://github.com/GMWalletApp/epusdt/releases/latest)

## 2.准备 SQLite 数据目录
EPUSDT 首次启动会创建 SQLite 文件和表。请确保运行用户对配置文件旁的主库目录及运行目录有写权限；如需另选位置，可设置下文的数据库路径。

## 3.配置反向代理
```bash
nano /etc/nginx/sites-enabled/epusdt
```
你可以参考以下我的配置文件，注意更改域名。
```bash
server {
   listen 80;
   server_name domain.com;
   return 301 https://domain.com$request_uri;
 }

server {
   listen 443 ssl http2;
   server_name domain.com;
   ssl_certificate  /etc/nginx/sslcert/cert.crt;
   ssl_certificate_key  /etc/nginx/sslcert/key.key; 
   ssl_prefer_server_ciphers on;

   location / {
        proxy_pass http://127.0.0.1:8000;
}
}

```
## 4.赋予Epusdt执行权限
`linux`服务器需要赋予`Epust`执行权限方可启动。            
执行命令```chmod +x epusdt```赋予权限
## 5、配置Epusdt
执行命令
```bash
mv .env.example .env
nano .env
```

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
runtime_root_path=./runtime
#日志配置
log_save_path=/logs
log_max_size=32
log_max_age=7
max_backups=3
# SQLite 数据文件
db_type=sqlite
sqlite_database_filename=epusdt.db
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
⚠️注意：配置文件里面不认识的不要修改，留空即可，不会改又要瞎改，除非你对项目源代码很熟悉很有信心😁

请按实际部署填写 app_uri 和 api_auth_token。

选填配置项：tg_bot_token、tg_manage
## 6、配置supervisor
为了保证`Epusdt`常驻后台运行，我们需要配置`supervisor`来实现进程监听  
```bash
nano /etc/supervisor/conf.d/epusdt.conf
```
你可以参考以下我的配置文件，注意更改路径。
```conf
[program:epusdt]
process_name=epusdt
directory=/var/www/epusdt
command=/var/www/epusdt/epusdt http start
autostart=true
autorestart=true
user=www-data
numprocs=1
redirect_stderr=true
stdout_logfile=/var/log/supervisor/epusdt.log
```
接下来输入命令
```bash
supervisorctl reread
supervisorctl update
supervisorctl start epusdt
supervisorctl tail epusdt
```
出现下图，即为配置成功
```bash
  _____                     _ _   
 | ____|_ __  _   _ ___  __| | |_ 
 |  _| | '_ \| | | / __|/ _` | __|
 | |___| |_) | |_| \__ \ (_| | |_ 
 |_____| .__/ \__,_|___/\__,_|\__|
       |_|                        
Epusdt version(0.0.2) Powered by GMWalletApp https://github.com/GMWalletApp/epusdt
⇨ http server started on [::]:8000
```
## 其他注意事项
- 1.所有`.env`配置文件有了修改后都需要重启supervisor进程 `supervisorctl restart epusdt`
- 2.教程所示的目录均为参考，请勿1:1照抄，根据自己实际情况来
