# Route APISIX (standalone). __APISIX_KEY__ diganti dari variabel APISIX_KEY saat container start.
upstreams:
  - id: catalog
    type: roundrobin
    nodes:
      "catalog:8080": 1
  - id: inventory
    type: roundrobin
    nodes:
      "inventory:8080": 1
  - id: order-lb
    # Gateway hanya mengenal satu alamat (nginx); nginx yang membagi ke 2 replica order.
    type: roundrobin
    nodes:
      "order-lb:80": 1
  - id: monitor
    type: roundrobin
    nodes:
      "monitor:8080": 1
  - id: bank
    type: roundrobin
    nodes:
      "bank:8080": 1

consumers:
  - username: storefront
    plugins:
      key-auth:
        key: __APISIX_KEY__

global_rules:
  - id: 1
    plugins:
      # Request id dibuat di pintu masuk lalu diteruskan ke semua service dan event NATS.
      request-id:
        header_name: X-Request-Id
        include_in_response: true
        algorithm: uuid

routes:
  - id: gw-ping
    uri: /gw/ping
    plugins:
      mocking:
        content_type: application/json
        response_status: 200
        response_example: '{"gateway":"apisix","ok":true}'

  - id: catalog
    uris: ["/api/catalog/*"]
    methods: [GET]
    upstream_id: catalog
    plugins:
      proxy-rewrite:
        regex_uri: ["^/api/catalog/(.*)", "/$1"]
      # Rate limiting: 20 request per 10 detik per alamat klien; kelebihan dijawab 429.
      limit-count:
        count: 20
        time_window: 10
        key_type: var
        key: remote_addr
        policy: local
        rejected_code: 429
        rejected_msg: "terlalu banyak request, coba lagi sebentar lagi"
        show_limit_quota_header: true

  - id: inventory
    uris: ["/api/inventory/*"]
    methods: [GET]
    upstream_id: inventory
    plugins:
      proxy-rewrite:
        regex_uri: ["^/api/inventory/(.*)", "/$1"]

  - id: orders
    uris: ["/api/orders", "/api/orders/*"]
    methods: [GET, POST]
    upstream_id: order-lb
    plugins:
      # Hanya klien yang membawa header apikey yang valid boleh membuat/melihat pesanan.
      key-auth:
        header: apikey
        hide_credentials: true
      proxy-rewrite:
        regex_uri: ["^/api/(orders.*)", "/$1"]

  - id: monitor
    uris: ["/api/monitor/*"]
    methods: [GET, POST]
    upstream_id: monitor
    plugins:
      key-auth:
        header: apikey
        hide_credentials: true
      proxy-rewrite:
        regex_uri: ["^/api/monitor/(.*)", "/$1"]

  # Layanan bank (bank + e-commerce). Port internal bank 8081 (debit belanja) TIDAK dirutekan.
  - id: bank
    uris: ["/api/bank/*"]
    methods: [GET, POST, PATCH]
    upstream_id: bank
    plugins:
      key-auth:
        header: apikey
        hide_credentials: true
      proxy-rewrite:
        regex_uri: ["^/api/bank/(.*)", "/$1"]
#END
