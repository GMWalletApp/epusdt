<p align="center"><img src="docs/assets/chainscanner.svg" width="96" height="96" alt=""></p>
<h1 align="center">ChainScanner</h1>
<p align="center"><strong>Отслеживайте адреса в блокчейне и получайте связанные с ними транзакции через WebSocket, gRPC или HTTP.</strong></p>
<p align="center"><a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.vi.md">Tiếng Việt</a> · <a href="README.es.md">Español</a> · <strong>Русский</strong></p>
<p align="center">Go 1.25+ &nbsp;·&nbsp; <a href="LICENSE">MIT</a> &nbsp;·&nbsp; 18 сетей</p>
<p align="center"><a href="docs/guide/ru.md#quick-start">Быстрый старт</a> &nbsp;·&nbsp; <a href="docs/guide/ru.md#networks">Сети</a> &nbsp;·&nbsp; <a href="docs/api/ru.md">Справочник по API</a></p>

---

## Подписка на адрес

Запустите локальный сервер, задайте `$TOKEN` из раздела быстрого старта и подставьте свой адрес в сети Base. Запрос заменяет весь список адресов этого потребителя.

```sh
curl -fsS -X PUT http://127.0.0.1:9081/v2/subscriptions \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '[{
    "network": "base",
    "address": "0x1111111111111111111111111111111111111111"
  }]'
```

Поле `from_height` в ответе указывает первый блок, с которого действует подписка. [Получение и подтверждение транзакций](docs/api/ru.md#streaming)

## Спасибо, что пользуетесь ChainScanner

Если вы используете ChainScanner или взяли его подход за основу, будем благодарны за ссылку на репозиторий в README вашего проекта. Для нашей команды такая рекомендация многое значит и помогает другим разработчикам найти ChainScanner. Рады, что наш проект вам пригодился!

```markdown
Источник: [ChainScanner](https://github.com/GMWalletApp/ChainScanner)
```
