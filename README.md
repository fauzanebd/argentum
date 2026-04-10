# Analytics Agent

An AI-powered analytics agent that allows business owners to ask natural-language questions via WhatsApp and receive direct answers, auto-generated dashboards, and shareable insights.

## Features

- **Natural Language Queries**: Ask questions about your business data in plain English
- **WhatsApp Integration**: Interact with your data through WhatsApp Business API
- **Auto-generated Dashboards**: Visualizations created automatically via Metabase
- **Smart Context Management**: 4-layer memory architecture with summarization
- **Semantic Caching**: Query meaning-based caching for 50-70% hit rates
- **Vector Database**: pgvector for conversation history and semantic search
- **Multi-Provider LLM**: OpenAI, custom endpoints, BYOM support with fallback
- **Production-Ready**: Monitoring, metrics, async processing, health checks

## Architecture

```
┌──────────────┐      ┌──────────────┐      ┌─────────────────┐
│   WhatsApp   │─────▶│  API Server  │─────▶│  Message Queue  │
│   Business   │      │   (Webhook)  │      │   (RabbitMQ)    │
└──────────────┘      └──────────────┘      └─────────────────┘
                                                   │
                       ┌───────────────────────────┘
                       ▼
              ┌──────────────────┐
              │  Agent Worker    │
              │  - LLM Agent     │
              │  - SQL Generator │
              │  - Query Executor│
              └──────────────────┘
                       │
       ┌───────────────┼───────────────┐
       ▼               ▼               ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│  PostgreSQL  │ │    Redis     │ │  Metabase    │
│  + pgvector  │ │   (Cache)    │ │ (Dashboards) │
└──────────────┘ └──────────────┘ └──────────────┘
```

## Quick Start

### Prerequisites

- Docker and Docker Compose
- Go 1.21+ (for local development)
- WhatsApp Business API credentials
- OpenAI API key (or custom LLM endpoint)

### 1. Clone and Configure

```bash
git clone <repository-url>
cd argentum

# Copy and edit environment file
cp .env.example .env
# Edit .env with your credentials
```

### 2. Start Infrastructure Services

```bash
docker-compose up -d postgres rabbitmq redis metabase
```

Wait for services to be ready:

```bash
docker-compose ps
```

### 3. Initialize Database

The migrations will run automatically when PostgreSQL starts. To verify:

```bash
docker-compose exec postgres psql -U analytics -d analytics_db -c "\dt"
```

### 4. Build and Run

Using Docker Compose (recommended):

```bash
docker-compose up --build api worker
```

Or locally:

```bash
# Install dependencies
go mod download

# Run API server
go run cmd/api/main.go

# In another terminal, run worker
go run cmd/worker/main.go
```

### 5. Configure WhatsApp Webhook

For local development, use ngrok to expose your local server:

```bash
docker-compose up -d ngrok
# Get the public URL
curl http://localhost:4040/api/tunnels
```

Then configure your WhatsApp Business webhook:

- Callback URL: `https://<ngrok-url>/webhook/whatsapp`
- Verify Token: Use the value from `WHATSAPP_WEBHOOK_VERIFY_TOKEN` in your `.env`

## Usage

Once everything is running, send a WhatsApp message to your business number:

**Example Queries:**

- "How much did we sell last month?"
- "What are our top 5 products?"
- "Show me sales by city"
- "Compare this month to last month"
- "Show me a pie chart of sales by category"
- "Create a dashboard of our top products"

## Project Structure

```
.
├── cmd/
│   ├── api/                    # WhatsApp webhook + monitoring endpoints
│   └── worker/                 # Queue consumer with agent
├── internal/
│   ├── agent/                  # Core analytics agent (Tool Registry)
│   ├── cache/                  # Redis multi-layer caching
│   ├── config/                 # Environment configuration
│   ├── context/                # Advanced conversation context
│   ├── database/               # PostgreSQL queries
│   ├── jobs/                   # Async job processing
│   ├── llm/                    # Multi-provider LLM + BYOM
│   ├── metabase/               # Dashboard generation
│   ├── metadata/               # Schema extraction
│   ├── metrics/                # Monitoring & observability
│   ├── queue/                  # RabbitMQ client
│   ├── semantic/               # Semantic caching
│   ├── tenant/                 # Multi-tenant support
│   ├── tools/                  # Tool Registry
│   ├── vector/                 # pgvector/Pinecone client
│   ├── visualization/          # Chart type detection
│   └── whatsapp/               # WhatsApp API client
├── pkg/models/                 # Shared data models
├── migrations/                 # Database migrations
├── docs/                       # Documentation
├── scripts/                    # Setup scripts
├── docker-compose.yml          # Infrastructure setup
└── README.md
```

## Environment Variables

### Required

| Variable                        | Description                        |
| ------------------------------- | ---------------------------------- |
| `LLM_API_KEY`                   | OpenAI API key (or custom LLM key) |
| `WHATSAPP_ACCESS_TOKEN`         | WhatsApp Business API token        |
| `WHATSAPP_PHONE_NUMBER_ID`      | WhatsApp phone number ID           |
| `WHATSAPP_APP_SECRET`           | WhatsApp app secret                |
| `WHATSAPP_WEBHOOK_VERIFY_TOKEN` | Webhook verification token         |

### Database

| Variable      | Description         | Default      |
| ------------- | ------------------- | ------------ |
| `DB_HOST`     | PostgreSQL host     | localhost    |
| `DB_PORT`     | PostgreSQL port     | 5432         |
| `DB_USER`     | PostgreSQL user     | analytics    |
| `DB_PASSWORD` | PostgreSQL password | analytics123 |
| `DB_NAME`     | PostgreSQL database | analytics_db |

### Vector Store

| Variable             | Description                                  | Default  |
| -------------------- | -------------------------------------------- | -------- |
| `VECTOR_STORE_TYPE`  | Vector store type (pgvector/pinecone/memory) | pgvector |
| `PINECONE_API_KEY`   | Pinecone API key (if using Pinecone)         | -        |
| `PINECONE_INDEX_URL` | Pinecone index URL                           | -        |

### LLM Configuration

| Variable                | Description                  | Default     |
| ----------------------- | ---------------------------- | ----------- |
| `LLM_PROVIDER`          | LLM provider (openai/custom) | openai      |
| `LLM_MODEL`             | Model name                   | gpt-4o-mini |
| `LLM_FALLBACK_PROVIDER` | Fallback provider            | -           |
| `LLM_FALLBACK_BASE_URL` | Custom LLM base URL          | -           |

### Application

| Variable            | Description               | Default                               |
| ------------------- | ------------------------- | ------------------------------------- |
| `REDIS_URL`         | Redis connection URL      | localhost:6380                        |
| `RABBITMQ_URL`      | RabbitMQ connection URL   | amqp://admin:admin123@localhost:5672/ |
| `METABASE_URL`      | Metabase URL              | http://localhost:3000                 |
| `CONTEXT_MAX_TURNS` | Max conversation turns    | 3                                     |
| `CACHE_TTL_SHORT`   | Short cache TTL (seconds) | 300                                   |
| `CACHE_TTL_LONG`    | Long cache TTL (seconds)  | 86400                                 |

See `.env.example` for all available options.

## API Endpoints

### Health & Monitoring

```bash
GET /health              # Health check
GET /ready               # Readiness probe
GET /metrics             # Prometheus metrics
```

### Job Tracking

```bash
GET /jobs/:id            # Check job status
GET /jobs/stats          # Job statistics
```

### WhatsApp Webhook

```bash
GET  /webhook/whatsapp   # Webhook verification
POST /webhook/whatsapp   # Message processing
```
