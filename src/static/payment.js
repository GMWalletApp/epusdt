/* =============================================================
 *  GM Pay — payment.js
 *  后端对接只需关注:
 *    1. CONFIG.api.supportedAssets — 获取支持的网络和币种
 *    2. CONFIG.api.selectMethod    — Step 1 确认接口（POST 币种/网络，返回收款地址）
 *    3. CONFIG.api.checkStatus     — 轮询接口 URL
 *    4. CONFIG.api.statusMap       — 状态码映射
 *    5. index.html 底部 <script> 中 ORDER 变量
 * ============================================================= */

'use strict';

// ── DOM helpers ───────────────────────────────────────────────
const $ = (id) => document.getElementById(id);
const $$ = (sel) => document.querySelectorAll(sel);
const setText = (id, value) => {
  const el = $(id);
  if (el) el.textContent = value;
  return el;
};
const setHtml = (id, value) => {
  const el = $(id);
  if (el) el.innerHTML = value;
  return el;
};

// ── API 错误码映射 ────────────────────────────────────────────
const API_ERRORS = {
  400:   { en: 'System error',                             zh: '系统错误',               'zh-hk': '系統錯誤',              ja: 'システムエラー',                        ko: '시스템 오류',                  ru: 'Системная ошибка' },
  401:   { en: 'Signature / authentication failed',        zh: '签名/认证失败',           'zh-hk': '簽名/認證失敗',          ja: '署名/認証に失敗しました',               ko: '서명/인증 실패',               ru: 'Ошибка подписи / аутентификации' },
  10001: { en: 'Wallet address already exists',            zh: '钱包地址已存在',          'zh-hk': '錢包地址已存在',         ja: 'ウォレットアドレスが既に存在します',    ko: '지갑 주소가 이미 존재합니다',  ru: 'Адрес кошелька уже существует' },
  10002: { en: 'Order already exists',                     zh: '订单已存在',              'zh-hk': '訂單已存在',             ja: '注文が既に存在します',                  ko: '주문이 이미 존재합니다',       ru: 'Заказ уже существует' },
  10003: { en: 'No wallet address available',              zh: '无可用钱包地址',          'zh-hk': '無可用錢包地址',         ja: '利用可能なウォレットアドレスがありません', ko: '사용 가능한 지갑 주소 없음', ru: 'Нет доступного адреса кошелька' },
  10004: { en: 'Invalid payment amount',                   zh: '无效支付金额',            'zh-hk': '無效支付金額',           ja: '無効な支払い金額',                      ko: '유효하지 않은 결제 금액',      ru: 'Недопустимая сумма платежа' },
  10005: { en: 'No available amount channel',              zh: '无可用金额通道',          'zh-hk': '無可用金額通道',         ja: '利用可能な金額チャンネルがありません',  ko: '사용 가능한 금액 채널 없음',   ru: 'Нет доступного канала суммы' },
  10006: { en: 'Exchange rate calculation failed',         zh: '汇率计算失败',            'zh-hk': '匯率計算失敗',           ja: '為替レート計算に失敗しました',          ko: '환율 계산 실패',               ru: 'Ошибка расчёта курса' },
  10007: { en: 'Block transaction already processed',      zh: '区块交易已处理',          'zh-hk': '區塊交易已處理',         ja: 'ブロックトランザクションは処理済みです', ko: '블록 트랜잭션이 이미 처리됨', ru: 'Транзакция блока уже обработана' },
  10008: { en: 'Order not found',                          zh: '订单不存在',              'zh-hk': '訂單不存在',             ja: '注文が見つかりません',                  ko: '주문 없음',                    ru: 'Заказ не найден' },
  10009: { en: 'Request parameter parsing failed',         zh: '请求参数解析失败',        'zh-hk': '請求參數解析失敗',       ja: 'リクエストパラメータの解析に失敗',      ko: '요청 매개변수 파싱 실패',      ru: 'Ошибка разбора параметров запроса' },
  10010: { en: 'Order status has changed',                 zh: '订单状态已变更',          'zh-hk': '訂單狀態已變更',         ja: '注文ステータスが変更されました',        ko: '주문 상태가 변경됨',           ru: 'Статус заказа изменился' },
  10011: { en: 'Sub-order quantity exceeded',              zh: '子订单数量超限',          'zh-hk': '子訂單數量超限',         ja: 'サブ注文数が超過しました',              ko: '하위 주문 수량 초과',          ru: 'Превышено количество подзаказов' },
  10012: { en: 'Cannot switch network for sub-orders',     zh: '不能对子订单切换网络',    'zh-hk': '不能對子訂單切換網絡',   ja: 'サブ注文のネットワーク切替不可',        ko: '하위 주문에 네트워크 전환 불가', ru: 'Нельзя переключить сеть для подзаказов' },
  10013: { en: 'Order is not in pending payment status',   zh: '订单不是待支付状态',      'zh-hk': '訂單不是待支付狀態',     ja: '注文は支払い待ち状態ではありません',    ko: '주문이 결제 대기 상태가 아님', ru: 'Заказ не в статусе ожидания оплаты' },
  10016: { en: 'Invalid wallet address',                   zh: '无效钱包地址',            'zh-hk': '無效錢包地址',           ja: '無効なウォレットアドレス',              ko: '유효하지 않은 지갑 주소',      ru: 'Недопустимый адрес кошелька' },
  10017: { en: 'Payment method unavailable',               zh: '支付方式不可用',          'zh-hk': '支付方式不可用',         ja: '支払い方法は利用できません',            ko: '결제 수단을 사용할 수 없음',   ru: 'Способ оплаты недоступен' },
};

/**
 * 根据 API 错误码返回当前语言的提示文字；未匹配时返回 null
 * @param {number|string|null|undefined} code
 * @returns {string|null}
 */
function getApiErrorMsg(code) {
  if (code == null) return null;
  const map = API_ERRORS[Number(code)];
  if (!map) return null;
  return map[currentLang] ?? map.en;
}

/** API 业务异常（区别于网络异常），携带错误码 */
class ApiError extends Error {
  constructor(code, message) { super(message); this.code = code; }
}

/** 错误码 → 终态面板映射；命中即跳转面板，不弹 toast */
const _TERMINAL_ERROR_CODES = {
  10008: 'not-found',   // 订单不存在
  10010: 'expired',     // 订单状态已变更
  10013: 'not-found',   // 订单不是待支付状态
};

/**
 * 统一 API 请求：发送 → 解析 JSON → 校验业务码 → 返回 data
 * 业务异常抛 ApiError，网络/超时异常原样冒泡
 */
async function apiFetch(url, opts = {}) {
  const { timeout = 10000, ...fetchOpts } = opts;
  fetchOpts.headers = { Accept: 'application/json', ...fetchOpts.headers };
  if (!fetchOpts.signal) fetchOpts.signal = AbortSignal.timeout(timeout);
  const res  = await fetch(url, fetchOpts);
  const resp = await res.json().catch(() => null);
  const code = resp?.status_code != null ? Number(resp.status_code) : res.status;
  if (!res.ok || (resp?.status_code != null && Number(resp.status_code) !== 200)) {
    throw new ApiError(code, getApiErrorMsg(code) ?? resp?.message ?? `HTTP ${res.status}`);
  }
  return resp?.data ?? resp;
}

/**
 * 统一处理 API 异常：终态错误码 → 跳转面板；其余 → 弹 toast
 * @returns {boolean} true = 终态，已跳转面板
 */
function handleApiError(err) {
  const panel = (err instanceof ApiError) && _TERMINAL_ERROR_CODES[err.code];
  if (panel) { _enterTerminalState(panel); return true; }
  console.error('[API]', err);
  showToast('⚠ ' + (err.message || t('network_timeout')));
  return false;
}

// ── 全局配置（可按项目调整）─────────────────────────────────
const CONFIG = {
  poll: {
    interval:  5000,   // 轮询间隔（毫秒）
    maxErrors: 5,      // 最多连续错误次数，超出进入 timeout 状态
    timeout:   8000,   // 单次请求超时（毫秒）
  },
  redirect: {
    delay: 3000,       // 支付成功后自动跳转延迟（毫秒）
  },
  // ---- 后端接口 ----
  api: {
    // 获取支持的网络和币种
    supportedAssets: (currency, amount) => {
      const qs = new URLSearchParams();
      if (currency) qs.set('currency', currency);
      if (amount) qs.set('amount', amount);
      const query = qs.toString();
      return query ? `/payments/gmpay/v1/supported-assets?${query}` : '/payments/gmpay/v1/supported-assets';
    },
    // 切换网络接口：POST { trade_id, token, network }，返回完整订单对象
    selectMethod: () => '/pay/switch-network',
    // 轮询接口：GET，返回 { data: { status: number } }
    checkStatus: (tradeId) => `/pay/check-status/${encodeURIComponent(tradeId)}`,
    // status 状态码映射
    statusMap: {
      paid:    2,   // 支付成功
      expired: 3,   // 已过期
    },
  },
  // ---- 连接钱包 ----
  wallet: {
    enabled: false,   // false = 隐藏「连接钱包支付」按钮
    // TronLink deeplink，如需支持其他钱包替换此处
    deeplink: (currentUrl) => {
      const param = JSON.stringify({
        url:       currentUrl,
        action:    'open',
        dapp_name: 'GM Pay',
        dapp_icon: 'https://www.gmwallet.app/favicon.png',
      });
      return 'tronlinkoutside://pull.activity?param=' + encodeURIComponent(param);
    },
  },
};


// ═══════════════════════════════════════════════════════════════
//  SECTION 1 — 链图标 / 网络渲染
// ═══════════════════════════════════════════════════════════════

const _CHAIN_PRESETS = {
  Ethereum: { bg: 'hsla(0,0%,55%,0.25)',     color: 'hsla(0,0%,55%,1)',     icon: 'ethereum' },
  Solana:   { bg: 'hsla(256,85%,65%,0.25)',  color: 'hsla(256,85%,65%,1)',  icon: 'solana'   },
  BSC:      { bg: 'hsla(46,91%,49%,0.25)',   color: 'hsla(46,91%,49%,1)',   icon: 'bsc'      },
  Arbitrum: { bg: 'hsla(202,100%,54%,0.25)', color: 'hsla(202,100%,54%,1)', icon: 'arbitrum' },
  TRON:     { bg: 'hsla(350,100%,47%,0.20)', color: 'hsla(350,100%,47%,1)', icon: 'tron'     },
  Polygon:  { bg: 'hsla(263,73%,56%,0.25)',  color: 'hsla(263,73%,56%,1)',  icon: 'polygon'  },
  Base:     { bg: 'hsla(240,100%,61%,0.25)', color: 'hsla(240,100%,61%,1)', icon: 'base'     },
  OP:       { bg: 'hsla(353,99%,51%,0.25)',  color: 'hsla(353,99%,51%,1)',  icon: 'op'       },
  HyperEVM: { bg: 'hsla(166,94%,79%,0.20)', color: 'hsla(166,94%,79%,1)',  icon: 'hyperevm' },
  Plasma:   { bg: 'hsla(166,64%,32%,0.25)',  color: 'hsla(166,64%,32%,1)',  icon: 'plasma'   },
  Bitcoin:  { bg: 'hsla(33,93%,54%,0.20)',   color: 'hsla(33,93%,54%,1)',   icon: 'bitcoin'  },
  Binance:  { bg: 'hsla(46,91%,49%,0.24)',   color: 'hsla(46,91%,49%,1)',   icon: 'binance'  },
};

const _CHAIN_ALIASES = {
  evm:'Ethereum', eth:'Ethereum', ethereum:'Ethereum',
  bsc:'BSC', bnb:'BSC',
  arbitrum:'Arbitrum', arb:'Arbitrum',
  sol:'Solana', solana:'Solana',
  tron:'TRON', trx:'TRON',
  polygon:'Polygon', matic:'Polygon', pol:'Polygon',
  base:'Base',
  op:'OP', optimism:'OP',
  hyperevm:'HyperEVM', hyperliquid:'HyperEVM', hype:'HyperEVM',
  plasma:'Plasma', xpl:'Plasma',
  bitcoin:'Bitcoin', btc:'Bitcoin',
  binance:'Binance', bnb_chain:'Binance',
};

const IMAGE_PREFIX = {
  chain: '/static/images/',
  token: 'https://cdn.jsdmirror.com/gh/atomiclabs/cryptocurrency-icons@1a63530/128/color/',
};

function _resolveChain(name) {
  if (!name) return null;
  const k = name.trim().toLowerCase();
  const canonical = _CHAIN_ALIASES[k] || Object.keys(_CHAIN_PRESETS).find(c => c.toLowerCase() === k);
  return canonical ? { label: canonical, ..._CHAIN_PRESETS[canonical] } : null;
}

/** 用于 order-info 顶部的网络 tag badge */
function networkTag(network) {
  const chain = _resolveChain(network);
  if (!chain) return `<span style="font-weight:600;color:var(--card-foreground)">${network}</span>`;
  return `<span style="display:inline-flex;align-items:center;gap:4px;padding:2px 8px 2px 4px;border-radius:99px;background:${chain.bg};color:${chain.color};font-size:12px;font-weight:600;line-height:1.6">
    <img src="${IMAGE_PREFIX.chain}${chain.icon}.png" width="14" height="14" style="width:14px;height:14px;border-radius:50%;object-fit:cover;flex-shrink:0" />
    ${chain.label}
  </span>`;
}

/** 选择器触发器显示：网络 */
function _networkTriggerHtml(network) {
  const chain = _resolveChain(network);
  if (!chain) return `<span class="text-sm font-semibold" style="color:var(--card-foreground)">${network || '--'}</span>`;
  return `<span style="display:inline-flex;align-items:center;gap:7px">
    <img src="${IMAGE_PREFIX.chain}${chain.icon}.png" width="18" height="18" style="width:18px;height:18px;border-radius:50%;object-fit:cover;flex-shrink:0" />
    <span class="text-sm font-semibold" style="color:var(--card-foreground)">${chain.label}</span>
  </span>`;
}

/** 选择器触发器显示：Token */
function _tokenTriggerHtml(token) {
  if (!token) return `<span class="text-sm font-semibold" style="color:var(--card-foreground)">--</span>`;
  const src = `${IMAGE_PREFIX.token}${token.toLowerCase()}.png`;
  return `<span style="display:inline-flex;align-items:center;gap:7px">
    <img src="${src}" width="18" height="18" style="width:18px;height:18px;border-radius:50%;object-fit:cover;flex-shrink:0" onerror="this.style.display='none'" />
    <span class="text-sm font-semibold" style="color:var(--card-foreground)">${token}</span>
  </span>`;
}

/** 下拉菜单中的网络选项 */
function _networkMenuItemHtml(opt, selected) {
  const chain = _resolveChain(opt.network);
  const icon = chain
    ? `<img src="${IMAGE_PREFIX.chain}${chain.icon}.png" width="16" height="16" style="width:16px;height:16px;border-radius:50%;object-fit:cover;flex-shrink:0" />`
    : '';
  return `<div class="select-option menu-item${selected ? ' is-selected' : ''}"
    onclick="step1SetNetwork('${opt.network}')"
    style="display:flex;align-items:center;gap:8px">${icon}${chain ? chain.label : opt.network}</div>`;
}


// ═══════════════════════════════════════════════════════════════
//  SECTION 2 — 主题 & Lucide 图标
// ═══════════════════════════════════════════════════════════════

if (typeof lucide !== 'undefined') lucide.createIcons();

let currentTheme = localStorage.getItem('theme') || 'dark';
applyTheme(currentTheme);

function applyTheme(t) {
  currentTheme = t;
  document.documentElement.setAttribute('data-theme', t);
  localStorage.setItem('theme', t);
  const moon = $('icon-moon'); if (moon) moon.style.display = t === 'light' ? 'block' : 'none';
  const sun  = $('icon-sun');  if (sun)  sun.style.display  = t === 'dark'  ? 'block' : 'none';
}

function toggleTheme(e) {
  const next = currentTheme === 'light' ? 'dark' : 'light';
  if (!document.startViewTransition) { applyTheme(next); return; }
  const x = e ? e.clientX : window.innerWidth / 2;
  const y = e ? e.clientY : window.innerHeight / 2;
  const r = Math.hypot(Math.max(x, window.innerWidth - x), Math.max(y, window.innerHeight - y));
  document.startViewTransition(() => applyTheme(next)).ready.then(() => {
    document.documentElement.animate(
      { clipPath: [`circle(0px at ${x}px ${y}px)`, `circle(${r}px at ${x}px ${y}px)`] },
      { duration: 420, easing: 'ease-in-out', pseudoElement: '::view-transition-new(root)' }
    );
  });
}


// ═══════════════════════════════════════════════════════════════
//  SECTION 3 — 国际化 (i18n)
// ═══════════════════════════════════════════════════════════════

const LANGS = {
  en: {
    scan_title:          'Scan or copy address to pay',
    amount_to_pay:       'Amount to pay',
    payment_address:     'Payment address',
    i_have_transferred:  'I have transferred',
    checking_blockchain: 'Checking blockchain…',
    expires:             'Expires',
    copied:              'Copied to clipboard',
    verifying:           'Verifying…',
    payment_success:     'Payment Successful',
    redirecting:         'Redirecting…',
    payment_expired:     'Payment Expired',
    expired_sub:         'Please initiate a new payment',
    order_id:            'Order ID',
    order_amount:        'Order amount',
    network_timeout:     'Connection Timeout',
    timeout_sub:         'Unable to connect to the payment server',
    retry:               'Retry',
    back:                'Back',
    order_not_found:     'Order Not Found',
    not_found_sub:       'The order does not exist or has already expired',
    select_method:       'Select payment method',
    currency_label:      'Currency',
    network_label:       'Network',
    confirm:             'Confirm',
    connect_wallet:      'Connect Wallet to Pay',
  },
  zh: {
    scan_title:          '扫码或复制地址付款',
    amount_to_pay:       '付款金额',
    payment_address:     '付款地址',
    i_have_transferred:  '我已转账',
    checking_blockchain: '链上核验中…',
    expires:             '到期时间',
    copied:              '已复制到剪贴板',
    verifying:           '核验中…',
    payment_success:     '支付成功',
    redirecting:         '正在跳转…',
    payment_expired:     '支付已过期',
    expired_sub:         '请重新发起支付',
    order_id:            '订单 ID',
    order_amount:        '订单金额',
    network_timeout:     '连接超时',
    timeout_sub:         '无法连接至支付服务器',
    retry:               '重试',
    back:                '返回',
    order_not_found:     '订单不存在',
    not_found_sub:       '待支付订单不存在或已过期',
    select_method:       '选择支付方式',
    currency_label:      '币种',
    network_label:       '网络',
    confirm:             '确认',
    connect_wallet:      '连接钱包支付',
  },
  ja: {
    scan_title:          'アドレスをスキャンまたはコピーして支払う',
    amount_to_pay:       '支払い金額',
    payment_address:     '支払いアドレス',
    i_have_transferred:  '送金しました',
    checking_blockchain: 'ブロックチェーン確認中…',
    expires:             '有効期限',
    copied:              'コピーしました',
    verifying:           '確認中…',
    payment_success:     '支払い完了',
    redirecting:         'リダイレクト中…',
    payment_expired:     '支払い期限切れ',
    expired_sub:         '新しい支払いを開始してください',
    order_id:            '注文 ID',
    order_amount:        '注文金額',
    network_timeout:     '接続タイムアウト',
    timeout_sub:         '支払いサーバーに接続できません',
    retry:               '再試行',
    back:                '戻る',
    order_not_found:     '注文が見つかりません',
    not_found_sub:       '注文が存在しないか、すでに期限切れです',
    select_method:       '支払い方法を選択',
    currency_label:      '通貨',
    network_label:       'ネットワーク',
    confirm:             '確認',
    connect_wallet:      'ウォレットで支払う',
  },
  ko: {
    scan_title:          '주소를 스캔하거나 복사하여 결제',
    amount_to_pay:       '결제 금액',
    payment_address:     '결제 주소',
    i_have_transferred:  '이체 완료',
    checking_blockchain: '블록체인 확인 중…',
    expires:             '만료 시간',
    copied:              '복사됨',
    verifying:           '확인 중…',
    payment_success:     '결제 성공',
    redirecting:         '리다이렉트 중…',
    payment_expired:     '결제 만료',
    expired_sub:         '새로운 결제를 시작하세요',
    order_id:            '주문 ID',
    order_amount:        '주문 금액',
    network_timeout:     '연결 시간 초과',
    timeout_sub:         '결제 서버에 연결할 수 없습니다',
    retry:               '다시 시도',
    back:                '돌아가기',
    order_not_found:     '주문 없음',
    not_found_sub:       '주문이 존재하지 않거나 이미 만료되었습니다',
    select_method:       '결제 수단 선택',
    currency_label:      '통화',
    network_label:       '네트워크',
    confirm:             '확인',
    connect_wallet:      '지갑으로 결제',
  },
  'zh-hk': {
    scan_title:          '掃碼或複製地址付款',
    amount_to_pay:       '付款金額',
    payment_address:     '付款地址',
    i_have_transferred:  '我已轉帳',
    checking_blockchain: '鏈上核驗中…',
    expires:             '到期時間',
    copied:              '已複製到剪貼簿',
    verifying:           '核驗中…',
    payment_success:     '支付成功',
    redirecting:         '正在跳轉…',
    payment_expired:     '支付已過期',
    expired_sub:         '請重新發起支付',
    order_id:            '訂單 ID',
    order_amount:        '訂單金額',
    network_timeout:     '連線逾時',
    timeout_sub:         '無法連線至支付伺服器',
    retry:               '重試',
    back:                '返回',
    order_not_found:     '訂單不存在',
    not_found_sub:       '待支付訂單不存在或已過期',
    select_method:       '選擇支付方式',
    currency_label:      '幣種',
    network_label:       '網絡',
    confirm:             '確認',
    connect_wallet:      '連接錢包支付',
  },
  ru: {
    scan_title:          'Отсканируйте или скопируйте адрес для оплаты',
    amount_to_pay:       'Сумма к оплате',
    payment_address:     'Адрес для оплаты',
    i_have_transferred:  'Я перевёл',
    checking_blockchain: 'Проверка блокчейна…',
    expires:             'Истекает',
    copied:              'Скопировано',
    verifying:           'Проверка…',
    payment_success:     'Оплата прошла успешно',
    redirecting:         'Перенаправление…',
    payment_expired:     'Срок оплаты истёк',
    expired_sub:         'Пожалуйста, создайте новый платёж',
    order_id:            'Заказ ID',
    order_amount:        'Сумма заказа',
    network_timeout:     'Тайм-аут подключения',
    timeout_sub:         'Не удалось подключиться к серверу',
    retry:               'Повторить',
    back:                'Назад',
    order_not_found:     'Заказ не найден',
    not_found_sub:       'Заказ не существует или уже истёк',
    select_method:       'Выберите способ оплаты',
    currency_label:      'Валюта',
    network_label:       'Сеть',
    confirm:             'Подтвердить',
    connect_wallet:      'Оплатить через кошелёк',
  },
};

const LANG_LABELS    = { en: 'EN', zh: '中文', 'zh-hk': '繁體', ja: '日本語', ko: '한국어', ru: 'RU' };
const SUPPORTED_LANGS = Object.keys(LANGS);
let currentLang = 'en';

const t = (key) => LANGS[currentLang]?.[key] ?? key;

function detectLang() {
  const saved = localStorage.getItem('lang');
  if (saved && SUPPORTED_LANGS.includes(saved)) return saved;
  for (const nav of (navigator.languages || [navigator.language])) {
    const lc = nav.toLowerCase();
    if (lc.startsWith('zh-hk') || lc.startsWith('zh-tw') || lc.startsWith('zh-mo')) return 'zh-hk';
    if (lc.startsWith('zh')) return 'zh';
    if (lc.startsWith('ja')) return 'ja';
    if (lc.startsWith('ko')) return 'ko';
    if (lc.startsWith('ru')) return 'ru';
    if (lc.startsWith('en')) return 'en';
  }
  return 'en';
}

function setLang(lang) {
  currentLang = lang;
  localStorage.setItem('lang', lang);
  $$('[data-i18n]').forEach(el => {
    const v = t(el.dataset.i18n);
    if (v) el.textContent = v;
  });
  setText('lang-label', LANG_LABELS[lang]);
  $$('#dd-lang-menu .select-option').forEach(o =>
    o.classList.toggle('is-selected', o.dataset.lang === lang));
  closeAllSelects();
  // 重新渲染含翻译文案的动态行
  renderOrderCard();
}


// ═══════════════════════════════════════════════════════════════
//  SECTION 4 — 下拉选择器
// ═══════════════════════════════════════════════════════════════

function toggleSelect(id) {
  const wrap    = $(id);
  const trigger = wrap.querySelector('.select-trigger');
  const menu    = wrap.querySelector('.select-menu');
  const wasOpen = menu.classList.contains('is-open');
  closeAllSelects();
  if (!wasOpen) {
    menu.classList.add('is-open');
    trigger?.classList.add('is-open');
  }
}

function closeAllSelects() {
  $$('.select-menu.is-open').forEach(m => {
    m.classList.remove('is-open');
    m.closest('.select-wrap')?.querySelector('.select-trigger')?.classList.remove('is-open');
  });
}

document.addEventListener('click', (e) => {
  if (!e.target.closest('.select-wrap')) closeAllSelects();
});


// ═══════════════════════════════════════════════════════════════
//  SECTION 5 — Toast & Clipboard
// ═══════════════════════════════════════════════════════════════

const CHECK_ICON = '<i data-lucide="check" width="15" height="15" stroke-width="2.5" color="#22c55e"></i>';

function showToast(msg) {
  const el = $('toast');
  if (!el) return;
  el.textContent = msg || t('copied');
  el.style.opacity   = '1';
  el.style.transform = 'translateX(-50%) translateY(0)';
  clearTimeout(el._tid);
  el._tid = setTimeout(() => {
    el.style.opacity   = '0';
    el.style.transform = 'translateX(-50%) translateY(10px)';
  }, 2000);
}

function flashCheck(btnId) {
  const btn = $(btnId);
  if (!btn) return;
  const orig = btn.innerHTML;
  btn.innerHTML = CHECK_ICON;
  lucide.createIcons({ nodes: [btn] });
  setTimeout(() => { btn.innerHTML = orig; lucide.createIcons({ nodes: [btn] }); }, 1800);
}

let _clipboard = null;

function initClipboard() {
  const targets = [
    { id: 'copy-addr-box',   text: ORDER.receiveAddress, iconId: 'btn-copy-addr'   },
    { id: 'btn-copy-amount', text: ORDER.actualAmount,   iconId: 'btn-copy-amount' },
  ];
  targets.forEach(({ id, text }) => $(id)?.setAttribute('data-clipboard-text', text));

  if (typeof ClipboardJS !== 'undefined') {
    _clipboard?.destroy();
    _clipboard = new ClipboardJS(targets.map(c => '#' + c.id).join(', '));
    _clipboard.on('success', e => {
      e.clearSelection();
      const tgt = targets.find(c => c.id === e.trigger.id);
      showToast();
      if (tgt) flashCheck(tgt.iconId);
    });
  }
}


// ═══════════════════════════════════════════════════════════════
//  SECTION 6 — 面板切换 & 导航
// ═══════════════════════════════════════════════════════════════

const PANEL_IDS = {
  step1:       'step1-panel',
  payment:     'payment-panel',
  success:     'screen-success',
  expired:     'screen-expired',
  timeout:     'screen-timeout',
  'not-found': 'screen-not-found',
};
const PANEL_ORDER = ['step1', 'payment', 'success', 'expired', 'timeout', 'not-found'];
let _currentPanel = 'step1';

function slideTo(name, dir) {
  const fromEl   = $(PANEL_IDS[_currentPanel]);
  const toEl     = $(PANEL_IDS[name]);
  const viewport = $('panel-viewport');
  if (!toEl || name === _currentPanel) return;

  const d = dir ?? (PANEL_ORDER.indexOf(name) >= PANEL_ORDER.indexOf(_currentPanel) ? 1 : -1);
  _currentPanel = name;

  if (viewport) viewport.style.overflowX = 'hidden'; // 动画期间裁剪

  if (fromEl) {
    Object.assign(fromEl.style, { position: 'absolute', top: '0', left: '0', width: '100%' });
    fromEl.style.animation = `${d > 0 ? 'slide-out-l' : 'slide-out-r'} 0.38s cubic-bezier(.4,0,.2,1) forwards`;
    fromEl.addEventListener('animationend', () => {
      fromEl.style.cssText = 'display:none';
      if (viewport) viewport.style.overflowX = '';
    }, { once: true });
  } else if (viewport) {
    setTimeout(() => { viewport.style.overflowX = ''; }, 420);
  }

  toEl.style.display   = '';
  toEl.style.animation = `${d > 0 ? 'slide-in-r' : 'slide-in-l'} 0.38s cubic-bezier(.4,0,.2,1) forwards`;
}

function showStatePanel(state) {
  const isNotFound = state === 'not-found';
  const orderInfo = $('order-info');
  const stepProgress = $('step-progress');
  if (orderInfo) orderInfo.style.display = isNotFound ? 'none' : '';
  if (stepProgress) stepProgress.style.display = isNotFound ? 'none' : '';
  slideTo(state);
}

function hideStatePanel() {
  const orderInfo = $('order-info');
  const stepProgress = $('step-progress');
  if (orderInfo) orderInfo.style.display = '';
  if (stepProgress) stepProgress.style.display = '';
  slideTo('payment', -1);
}

function goBack() {
  if (ORDER?.redirectUrl && !ORDER.redirectUrl.startsWith('{{')) {
    window.location.href = ORDER.redirectUrl;
  } else if (document.referrer) {
    window.location.href = document.referrer;
  } else {
    history.back();
  }
}


// ═══════════════════════════════════════════════════════════════
//  SECTION 7 — 步骤进度条
// ═══════════════════════════════════════════════════════════════

function setStepBar(step) {
  const f1 = $('step-fill-1');
  const f2 = $('step-fill-2');
  if (!f1) return;
  const BASE = 'height:100%;border-radius:9999px;background:var(--foreground);';
  if (step === 1) {
    f1.style.cssText = BASE + 'animation:fill-bar 0.9s ease-out forwards';
    f2.style.cssText = BASE + 'width:0%';
  } else {
    f1.style.cssText = BASE + 'width:100%';
    f2.style.cssText = BASE + 'animation:fill-bar 0.9s ease-out forwards';
  }
}


// ═══════════════════════════════════════════════════════════════
//  SECTION 8 — Step 1 — 币种 & 网络选择
// ═══════════════════════════════════════════════════════════════

/**
 * 从后端获取支持的网络和币种，转换为 PAYMENT_OPTIONS 格式
 * @returns {Array|null} 成功返回选项数组，失败返回 null
 */
async function fetchSupportedAssets() {
  try {
    const data = await apiFetch(CONFIG.api.supportedAssets(ORDER.currency, ORDER.amount));
    if (!data?.supports?.length) return null;
    return data.supports.flatMap(s => s.tokens.map(token => ({ token, network: s.network })));
  } catch (e) {
    return null;
  }
}

function _normalizePaymentOptions(options) {
  if (!Array.isArray(options)) return [];
  const seen = new Set();
  return options.reduce((rows, option) => {
    const token = String(option?.token ?? '').trim().toUpperCase();
    const network = String(option?.network ?? '').trim().toLowerCase();
    if (!token || !network) return rows;
    const key = `${network}:${token}`;
    if (seen.has(key)) return rows;
    seen.add(key);
    rows.push({ token, network });
    return rows;
  }, []);
}

async function ensurePaymentOptions() {
  let options = _normalizePaymentOptions(window.PAYMENT_OPTIONS);
  if (options.length) return options;
  options = _normalizePaymentOptions(await fetchSupportedAssets());
  if (options.length) return options;
  options = _normalizePaymentOptions([{ token: ORDER.token, network: ORDER.network }]);
  return options;
}

let _step1Token = null;
let _step1Opt   = null;

function initStep1() {
  _step1Opt = PAYMENT_OPTIONS.find(o =>
    o.network === String(ORDER.network || '').trim().toLowerCase() &&
    o.token === String(ORDER.token || '').trim().toUpperCase()
  ) ?? PAYMENT_OPTIONS[0];
  _step1Token = _step1Opt.token;
  _renderNetworkMenu();
  _renderTokenMenu();
}

function _renderTokenMenu() {
  // 按当前选中网络过滤可用币种
  const opts = PAYMENT_OPTIONS.filter(o => o.network === _step1Opt?.network);
  const tokens = [...new Set(opts.map(o => o.token))];
  // 若当前 token 在新网络下不存在则重置为第一个
  if (!tokens.includes(_step1Token)) {
    _step1Token = tokens[0] ?? null;
    _step1Opt   = opts[0] ?? _step1Opt;
  }
  const menu = $('dd-token-menu');
  if (!menu) return;
  menu.innerHTML = tokens.map(tok => {
    const src = `${IMAGE_PREFIX.token}${tok.toLowerCase()}.png`;
    return `<div class="select-option menu-item${tok === _step1Token ? ' is-selected' : ''}"
      onclick="step1SetToken('${tok}')"
      style="display:flex;align-items:center;gap:8px">
      <img src="${src}" width="16" height="16" style="width:16px;height:16px;border-radius:50%;object-fit:cover;flex-shrink:0" onerror="this.style.display='none'" />
      ${tok}
    </div>`;
  }).join('');
  setHtml('token-label', _tokenTriggerHtml(_step1Token));
}

function _renderNetworkMenu() {
  // 去重，每个网络只显示一项
  const seen = new Set();
  const networkOpts = PAYMENT_OPTIONS.filter(o => {
    if (seen.has(o.network)) return false;
    seen.add(o.network);
    return true;
  });
  const menu = $('dd-network-menu');
  if (menu) menu.innerHTML = networkOpts.map(opt => _networkMenuItemHtml(opt, opt.network === _step1Opt?.network)).join('');
  setHtml('network-label', _networkTriggerHtml(_step1Opt?.network || '--'));
}

function step1SetToken(tok) {
  _step1Token = tok;
  // 在当前网络下找对应 opt
  _step1Opt = PAYMENT_OPTIONS.find(o => o.network === _step1Opt?.network && o.token === tok) ?? _step1Opt;
  setHtml('token-label', _tokenTriggerHtml(tok));
  $$('#dd-token-menu .select-option').forEach(o =>
    o.classList.toggle('is-selected', o.textContent.trim() === tok));
  closeAllSelects();
}

function step1SetNetwork(networkName) {
  // 找该网络下第一个 opt 作为默认
  _step1Opt = PAYMENT_OPTIONS.find(o => o.network === networkName) ?? _step1Opt;
  const chain = _resolveChain(networkName);
  const label = chain ? chain.label : networkName;
  $$('#dd-network-menu .select-option').forEach(o =>
    o.classList.toggle('is-selected', o.textContent.trim() === label));
  setHtml('network-label', _networkTriggerHtml(_step1Opt.network));
  _renderTokenMenu();
  closeAllSelects();
}

/** 用后端返回的订单字段更新 ORDER */
function _applyOrderData(data, opt) {
  ORDER.tradeId = data?.trade_id ?? ORDER.tradeId;
  if (data?.trade_id) {
    const _url = new URL(window.location.href);
    _url.pathname = _url.pathname.replace(/\/[^/]+$/, '/' + encodeURIComponent(data.trade_id));
    history.replaceState(null, '', _url.toString());
    renderOrderCard();
  }
  ORDER.token          = data?.token           ?? opt.token;
  ORDER.network        = data?.network         ?? opt.network;
  ORDER.actualAmount   = data?.actual_amount   != null ? String(data.actual_amount) : ORDER.actualAmount;
  ORDER.amount         = data?.amount          != null ? String(data.amount)         : ORDER.amount;
  ORDER.currency       = data?.currency        ?? ORDER.currency;
  ORDER.receiveAddress = data?.receive_address ?? ORDER.receiveAddress;
  if (data?.expiration_time != null) ORDER.expirationTime = String(data.expiration_time);
  if (data?.created_at      != null) ORDER.createdAt      = String(data.created_at);
  if (data?.redirect_url)            ORDER.redirectUrl    = data.redirect_url;
}

async function confirmStep1() {
  const opt = _step1Opt;
  const btn  = $('btn-confirm-step1');
  const span = btn?.querySelector('span') ?? btn;
  const origText = span?.textContent;

  // 1. 乐观 UI：禁用按钮防止重复提交
  if (btn) { btn.disabled = true; if (span) span.textContent = '…'; }

  try {
    const data = await apiFetch(CONFIG.api.selectMethod(), {
      method:  'POST',
      headers: { 'Content-Type': 'application/json' },
      body:    JSON.stringify({
        trade_id: ORDER.tradeId,
        token:    opt.token.toLowerCase(),
        network:  opt.network.toLowerCase(),
      }),
    });

    _applyOrderData(data, opt);
    setStepBar(2);
    slideTo('payment');
    initOrder();
  } catch (err) {
    handleApiError(err);
  } finally {
    if (btn) { btn.disabled = false; if (span && origText) span.textContent = origText; }
  }
}


// ═══════════════════════════════════════════════════════════════
//  SECTION 9 — 订单渲染 & 倒计时
// ═══════════════════════════════════════════════════════════════

function formatAddr(addr) {
  if (!addr || addr.length <= 10) return addr;
  return `<span style="font-weight:800;color:var(--primary)">${addr.slice(0, 4)}</span>`
       + `<span style="color:var(--muted-foreground)">${addr.slice(4, -6)}</span>`
       + `<span style="font-weight:800;color:var(--primary)">${addr.slice(-6)}</span>`;
}

function renderRow(id, label, value) {
  const el = $(id);
  if (el) el.innerHTML = `
    <td style="width:1%;white-space:nowrap;padding-right:0.75em">${label}</td>
    <td style="color:var(--card-foreground);font-weight:500;word-break:break-all">${value}</td>`;
}

/** 填充订单信息卡片（订单号 + 法币金额行） */
function renderOrderCard() {
  if (ORDER?.tradeId && !ORDER.tradeId.startsWith('{{')) {
    renderRow('display-order-id', t('order_id'), ORDER.tradeId);
  }
  if (ORDER?.amount && !ORDER.amount.startsWith('{{')) {
    renderRow('display-fiat', t('order_amount'), `${ORDER.amount} ${ORDER.currency || ''}`);
  }
}

function initOrder() {
  if (!ORDER?.tradeId || ORDER.tradeId.startsWith('{{')) { showNotFound(); return; }

  setText('display-amount', `${ORDER.actualAmount} ${ORDER.token}`);
  setHtml('field-address', formatAddr(ORDER.receiveAddress));
  setHtml('display-network', networkTag(ORDER.network));
  renderOrderCard();

  const qrcodeEl = $('qrcode');
  if (qrcodeEl) {
    qrcodeEl.innerHTML = ''; // 防止重复调用时叠加渲染
    new QRCode(qrcodeEl, {
      text:         ORDER.receiveAddress,
      width:        176,
      height:       176,
      colorDark:    '#111111',
      colorLight:   '#ffffff',
      correctLevel: QRCode.CorrectLevel.M,
    });
  }

  initCountdown();
  checkOrderStatus();
  initClipboard();
}

// 倒计时内部状态
const CIRCUMFERENCE = 2 * Math.PI * 20;
const _pad = (n) => String(n).padStart(2, '0');
let _countdownInterval = null;
let _totalSeconds      = 0;
let _expiresAt         = null;

function _lerpColor(a, b, ratio) {
  return `rgb(${[0,1,2].map(i => Math.round(a[i] + (b[i] - a[i]) * ratio)).join(',')})`;
}
function _ratioColor(ratio) {
  if (ratio >= 0.5) return '#22c55e';
  if (ratio >= 0.2) return _lerpColor([34,197,94], [249,115,22], (0.5 - ratio) / 0.3);
  return _lerpColor([249,115,22], [239,68,68], (0.2 - ratio) / 0.2);
}

function initCountdown() {
  // 清除上一次的倒计时（initOrder 可能被重复调用）
  if (_countdownInterval) { clearInterval(_countdownInterval); _countdownInterval = null; }
  const parseTime = (raw) => new Date(/^\d+$/.test(String(raw)) ? Number(raw) : raw);
  _expiresAt = parseTime(ORDER.expirationTime);
  const remaining = Math.max(0, Math.round((_expiresAt - Date.now()) / 1000));
  if (remaining <= 0) { showExpired(); return; }

  const created = parseTime(ORDER.createdAt);
  _totalSeconds = (!isNaN(created) && !isNaN(_expiresAt))
    ? Math.max(1, Math.round((_expiresAt - created) / 1000))
    : 600;

  $('ring').style.strokeDasharray = CIRCUMFERENCE;
  _expiresAt = new Date(_expiresAt.getTime() - 1000); // 避免初始渲染停顿
  _tickCountdown();
  _countdownInterval = setInterval(_tickCountdown, 1000);
}

function _tickCountdown() {
  const remaining = Math.max(0, Math.round((_expiresAt - Date.now()) / 1000));
  const h = Math.floor(remaining / 3600);
  const m = Math.floor((remaining % 3600) / 60);
  const s = remaining % 60;
  const timeStr = h > 0 ? `${_pad(h)}:${_pad(m)}:${_pad(s)}` : `${_pad(m)}:${_pad(s)}`;
  const color   = _ratioColor(_totalSeconds > 0 ? remaining / _totalSeconds : 0);
  const offset  = CIRCUMFERENCE * (1 - (_totalSeconds > 0 ? remaining / _totalSeconds : 0));

  const cdEl   = $('countdown');
  const ringEl = $('ring');
  if (cdEl)   { cdEl.textContent = timeStr; cdEl.style.color = color; }
  if (ringEl) { ringEl.style.stroke = color; ringEl.style.strokeDashoffset = offset; }

  const inlineEl = $('countdown-inline');
  if (inlineEl) { inlineEl.textContent = timeStr; inlineEl.style.color = color; }

  if (remaining <= 0) { clearInterval(_countdownInterval); showExpired(); }
}


// ═══════════════════════════════════════════════════════════════
//  SECTION 10 — API 轮询
//  后端对接：修改 CONFIG.api.checkStatus 与 CONFIG.api.statusMap
// ═══════════════════════════════════════════════════════════════

let _pollTimer   = null;
let _pollStopped = false;
let _pollErrors  = 0;

async function checkOrderStatus() {
  if (_pollStopped || !ORDER?.tradeId || ORDER.tradeId.startsWith('{{')) return;

  try {
    const data = await apiFetch(CONFIG.api.checkStatus(ORDER.tradeId), {
      timeout: CONFIG.poll.timeout,
    });
    _pollErrors = 0;
    const { paid, expired } = CONFIG.api.statusMap;
    if (data?.status === paid)         onPaymentSuccess();
    else if (data?.status === expired) showExpired();
    else                               _scheduleNextPoll();
  } catch (err) {
    if (err instanceof ApiError) {
      _pollErrors = 0;
      if (!handleApiError(err)) _scheduleNextPoll();
    } else {
      // 网络 / 超时异常：累计错误次数
      if (++_pollErrors >= CONFIG.poll.maxErrors) showTimeout();
      else _scheduleNextPoll();
    }
  }
}

function _scheduleNextPoll() {
  if (!_pollStopped) _pollTimer = setTimeout(checkOrderStatus, CONFIG.poll.interval);
}

function stopPolling() {
  _pollStopped = true;
  clearTimeout(_pollTimer);
}

/** 重置轮询状态并立即发起一次请求 */
function _resumePolling() {
  _pollErrors  = 0;
  _pollStopped = false;
  clearTimeout(_pollTimer);
  checkOrderStatus();
}

function retryPolling() {
  hideStatePanel();
  _resumePolling();
}


// ═══════════════════════════════════════════════════════════════
//  SECTION 11 — 支付状态处理
// ═══════════════════════════════════════════════════════════════

/** 进入终态面板：停止轮询，可选清除倒计时 / 禁用转账按钮 */
function _enterTerminalState(panel, { clearTimer = true, disableBtn = true } = {}) {
  stopPolling();
  if (clearTimer && _countdownInterval) { clearInterval(_countdownInterval); _countdownInterval = null; }
  if (disableBtn) { const btn = $('btn-transferred'); if (btn) btn.disabled = true; }
  showStatePanel(panel);
}

function onPaymentSuccess() {
  showSuccess(true);
}

function showSuccess(redirect = false) {
  _enterTerminalState('success');
  if (redirect && ORDER?.redirectUrl && !ORDER.redirectUrl.startsWith('{{')) {
    setTimeout(() => { window.location.href = ORDER.redirectUrl; }, CONFIG.redirect.delay);
  }
}

function showExpired()  { _enterTerminalState('expired'); }
function showTimeout()  { _enterTerminalState('timeout', { clearTimer: false, disableBtn: false }); }
function showNotFound() { _enterTerminalState('not-found'); }


// ═══════════════════════════════════════════════════════════════
//  SECTION 12 — 转账按钮 & 连接钱包
// ═══════════════════════════════════════════════════════════════

function handleTransfer() {
  const btn  = $('btn-transferred');
  if (!btn) return;
  const span = btn.querySelector('span');
  if (!span) return;
  span.textContent = t('verifying');
  btn.disabled     = true;
  _resumePolling();
  setTimeout(() => {
    span.textContent = t('i_have_transferred');
    btn.disabled     = false;
  }, 4000);
}

function connectWallet() {
  window.location.href = CONFIG.wallet.deeplink(window.location.href);
}


// ═══════════════════════════════════════════════════════════════
//  SECTION 13 — 初始化入口
// ═══════════════════════════════════════════════════════════════

document.addEventListener('DOMContentLoaded', async () => {
  setLang(detectLang());

  if (!ORDER?.tradeId || ORDER.tradeId.startsWith('{{')) {
    showNotFound();
    return;
  }

  // 在 Step 1 阶段就填充订单信息卡片
  renderOrderCard();
  if (ORDER.amount && !ORDER.amount.startsWith('{{')) {
    setText('display-amount', `${ORDER.amount} ${ORDER.currency || ''}`);
  }

  const status = Number(ORDER?.status || 0);
  const { paid, expired } = CONFIG.api.statusMap;
  if (status === paid) {
    showSuccess(false);
    return;
  }
  if (status === expired) {
    showExpired();
    return;
  }

  window.PAYMENT_OPTIONS = await ensurePaymentOptions();
  if (!PAYMENT_OPTIONS?.length) {
    showNotFound();
    return;
  }

  // isselect=true 时跳过 Step 1，直接进入支付面板
  const _isSelect = ORDER.is_selected && ORDER.is_selected !== 'false' && !ORDER.is_selected.startsWith('{{');
  if (_isSelect) {
    $('step1-panel').style.display = 'none';
    $('payment-panel').style.display = '';
    _currentPanel = 'payment';
    setStepBar(2);
    initOrder();
  } else {
    initStep1();
    setStepBar(1);
  }

  // 连接钱包按钮开关
  const walletBtn = $('btn-connect-wallet');
  if (walletBtn) walletBtn.style.display = CONFIG.wallet.enabled ? '' : 'none';
});
