<p align="center"><img src="docs/assets/chainscanner.svg" width="96" height="96" alt="ChainScanner"></p>
<h1 align="center">ChainScanner</h1>
<p align="center"><strong>Отслеживайте адреса в блокчейне и получайте связанные с ними транзакции через WebSocket, gRPC или HTTP.</strong></p>
<p align="center"><a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.vi.md">Tiếng Việt</a> · <a href="README.es.md">Español</a> · <strong>Русский</strong></p>
<p align="center">Go 1.25+ &nbsp;·&nbsp; <a href="LICENSE">AGPL-3.0</a> &nbsp;·&nbsp; 18 сетей</p>
<p align="center"><a href="docs/guide/ru.md#quick-start">Быстрый старт</a> &nbsp;·&nbsp; <a href="docs/guide/ru.md#networks">Сети</a> &nbsp;·&nbsp; <a href="docs/api/ru.md">Справочник по API</a></p>

---

## Зачем мы создали ChainScanner

При разработке GM Wallet и GMPay мы постоянно сталкивались с одной задачей. Кошельку нужно было знать, балансы каких адресов пора обновить, а платёжному сервису — когда на его адресах появляются новые транзакции. Если каждое приложение отдельно опрашивает RPC по каждому адресу, оба расходуют квоту на поиск одной и той же транзакции.

ChainScanner сканирует блоки каждой сети сразу для всех подписчиков. Каждый сервис получает транзакции по своим адресам и самостоятельно подтверждает их обработку. Кошелёк может обновить только затронутые балансы, а GMPay — проверить соответствующие заказы. При повторном чтении одних и тех же четырёх блоков основной сети Ethereum потребовалось по 11 RPC-запросов как для одного адреса, так и для 100 адресов, даже когда на них подписались два сервиса.

Каждое приложение отвечает за свою бизнес-логику, статусы заказов и балансы кошельков. ChainScanner предоставляет общую инфраструктуру для подписки на адреса, сканирования блоков и доставки событий.

> [!IMPORTANT]
> Поиск по хешу отдаёт предпочтение резервным узлам и возвращает первую корректную транзакцию. Узлы, предоставившие данные при сканировании, исключаются. [Сравнить провайдеров резервных узлов](#fallback-providers)

## Примеры применения

- **Приём платежей.** [Интеграция EPUSDT](https://github.com/GMWalletApp/epusdt/pull/127) отслеживает адреса оплаты и проверяет транзакцию через строгий запрос перед зачислением.
- **Идея для Telegram.** Бот может сохранять события личного кошелька и присылать хеш и направление перевода.
- **Идея для сверки.** Бухгалтерия и сервис уведомлений могут наблюдать за одними кошельками и подтверждать обработку независимо.

## Подписка на адрес

Запустите локальный сервер, задайте `$TOKEN` из раздела быстрого старта и подставьте свой адрес в сети Base. Запрос заменяет весь список адресов этого потребителя.

```sh
curl -fsS -X PUT http://127.0.0.1:9081/v1/subscriptions \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '[{
    "network": "base",
    "address": "0x1111111111111111111111111111111111111111"
  }]'
```

Поле `from_height` в ответе указывает первый блок, с которого действует подписка. [Получение и подтверждение транзакций](docs/api/ru.md#streaming)

## Рекомендуемый резервный узел

Регистрируйте резервные узлы по отдельности. Пропущенные диапазоны распределяются между доступными резервными узлами. [API](docs/api/ru.md#fallback)

```sh
BASE=http://127.0.0.1:9081
ADMIN_TOKEN=$(jq -er '.admin_token' chainscanner.json)
curl -fsS -X POST "$BASE/v1/admin/nodes/fallback" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"network":"base","protocol":"http","url":"https://paid-rpc.example.org","api_key":"YOUR_PROVIDER_KEY"}'
```

<a id="fallback-providers"></a>

### Поставщики резервных узлов

Сведения проверены 2026-11-21. Выбирайте самостоятельно. ChainScanner не гарантирует доступность, точность данных и цены. Стоимость вызова в кредитах, CU или RU зависит от метода. Уточняйте поддержку сети, истории и протокола.

| Сайт | Основание или запуск и профиль | Бесплатная квота |
| --- | --- | --- |
| [Ankr](https://www.ankr.com/) | [2017 · распределённая инфраструктура узлов](https://www.ankr.com/ankr-whitepaper-2.0.pdf) | [200 млн API-кредитов в месяц на Freemium](https://www.ankr.com/docs/rpc-service/charging-policy/) |
| [Infura](https://www.infura.io/) | [2016 · инфраструктура Ethereum и IPFS от Consensys](https://consensys.io/blog/press-release-infura-a-consensys-company-launches-infura-for-improved-ethereum-infrastructure-support) | [3 млн кредитов в сутки на Core по документации](https://docs.infura.io/get-started/pricing/) |
| [PublicNode](https://www.publicnode.com/) | [2018 · начало работы оператора Allnodes](https://www.allnodes.com/about/) | [Общие публичные RPC, фиксированная месячная квота на пользователя не опубликована](https://www.publicnode.com/) |
| [QuickNode](https://www.quicknode.com/) | [2017 · управляемые RPC и потоки данных](https://www.quicknode.com/about) | [10 млн API-кредитов на 30 дней пробного доступа](https://support.quicknode.com/articles/5387074982-1-month-free-trial) |
| [Alchemy](https://www.alchemy.com/) | [2017 · API для разработчиков и доступ к узлам](https://www.alchemy.com/company) | [30 млн CU в месяц](https://www.alchemy.com/docs/reference/pricing-plans) |
| [BlockPI](https://blockpi.io/) | [2022-10-25 · запуск альфа-версии распределённой RPC-сети](https://docs.blockpi.io/welcome-to-blockpi/about-blockpi) | [50 млн RU в месяц после регистрации](https://docs.blockpi.io/pricing/pricing-and-rate-limit) |
| [dRPC](https://drpc.org/) | [2023-03 · открыт публичный доступ к децентрализованному шлюзу RPC](https://blog.drpc.org/how-drpcs-free-tier-works/) | [50 млн CU за 30 дней](https://drpc.org/docs/howitworks/ratelimiting) |

## Спасибо, что пользуетесь ChainScanner

Если вы используете ChainScanner или взяли его подход за основу, будем благодарны за ссылку на репозиторий в README вашего проекта. Для нашей команды такая рекомендация многое значит и помогает другим разработчикам найти ChainScanner. Рады, что наш проект вам пригодился!

```markdown
Источник: [ChainScanner](https://github.com/GMWalletApp/ChainScanner)
```

Спасибо imToken и Bitget Wallet за публичные RPC, а ChainList, OpenChainBench и CompareNodes — за каталоги узлов. Благодарим всех пользователей за доверие.
