$stdout.sync = true

require 'keenetic'
require 'net/http'
require 'uri'
require 'logger'

LOGGER = Logger.new($stdout)
LOGGER.progname = 'keenetic-collector'

KEENETIC_HOST = ENV.fetch('KEENETIC_HOST', '192.168.1.1')
VM_PUSH_URL   = URI.parse(ENV.fetch('VM_PUSH_URL', 'http://victoriametrics:8428/api/v1/import/prometheus'))
INTERVAL      = ENV.fetch('COLLECT_INTERVAL', '60').to_i

Keenetic.configure do |config|
  config.host     = KEENETIC_HOST
  config.login    = ENV.fetch('KEENETIC_LOGIN', 'admin')
  config.password = ENV.fetch('KEENETIC_PASSWORD', '')
end

def push_metrics(lines)
  http = Net::HTTP.new(VM_PUSH_URL.host, VM_PUSH_URL.port)
  req  = Net::HTTP::Post.new(VM_PUSH_URL.path)
  req['Content-Type'] = 'text/plain'
  req.body = lines.join("\n")
  response = http.request(req)
  response.code == '204'
end

def collect_and_push(client)
  resources = client.system.resources

  cpu    = resources[:cpu]
  mem    = resources[:memory]
  router = KEENETIC_HOST

  lines = [
    "keenetic_cpu_load_percent{router=\"#{router}\"}     #{cpu[:load_percent]}",
    "keenetic_memory_total_bytes{router=\"#{router}\"}   #{mem[:total]}",
    "keenetic_memory_used_bytes{router=\"#{router}\"}    #{mem[:used]}",
    "keenetic_memory_free_bytes{router=\"#{router}\"}    #{mem[:free]}",
    "keenetic_memory_used_percent{router=\"#{router}\"} #{mem[:used_percent]}",
    "keenetic_uptime_seconds{router=\"#{router}\"}       #{resources[:uptime]}"
  ]

  if push_metrics(lines)
    LOGGER.info "pushed #{lines.size} metrics (cpu=#{cpu[:load_percent]}%, mem=#{mem[:used_percent]}%)"
  else
    LOGGER.warn 'push returned non-204, metrics may not have been stored'
  end
rescue Keenetic::AuthenticationError => e
  LOGGER.error "authentication failed: #{e.message}"
rescue Keenetic::ConnectionError, Keenetic::TimeoutError => e
  LOGGER.warn "router unreachable: #{e.message}"
rescue StandardError => e
  LOGGER.error "unexpected error: #{e.class}: #{e.message}"
end

# Wait for VM to be ready on startup
LOGGER.info "starting — collecting every #{INTERVAL}s from #{KEENETIC_HOST}"
sleep 5

client = Keenetic.client

loop do
  collect_and_push(client)
  sleep INTERVAL
end
