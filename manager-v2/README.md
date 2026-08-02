# AstraCalls Manager v2

Painel técnico para administrar sessões e testar a API do AstraCalls.

## Escopo

O Manager v2 não é uma caixa de entrada, CRM ou painel de atendimento. Ele serve para:

- autenticar o único administrador do painel;
- configurar a URL da API;
- criar, selecionar, parear, desconectar e excluir sessões;
- exibir QR code e estado da conexão em tempo real via SSE;
- testar chamadas e todos os formatos de mensagem disponíveis na API;
- editar o corpo JSON antes de cada requisição;
- visualizar status HTTP, duração e JSON de resposta;
- acompanhar eventos e erros para depuração.

As conversas e respostas operacionais continuam sendo tratadas por sistemas externos através da API, webhooks e eventos.

## Autenticação do administrador

O painel trabalha em modo **single-admin**:

1. no primeiro acesso, `GET /api/auth/status` informa que ainda não existe administrador;
2. a tela solicita usuário, senha e confirmação;
3. `POST /api/auth/setup` cria o registro de ID fixo `1`;
4. qualquer nova tentativa de cadastro recebe `409 Conflict`;
5. os próximos acessos usam `POST /api/auth/login`;
6. o JWT é enviado em `Authorization: Bearer <token>` para API e SSE.

A senha é armazenada somente como hash bcrypt. A senha mínima tem 12 caracteres. O JWT fica no `sessionStorage` do navegador, não no `localStorage`, e nunca é enviado pela URL.

Após cinco tentativas de login com falha dentro de dez minutos, o endereço de origem fica bloqueado por quinze minutos.

### Endpoints de autenticação

- `GET /api/auth/status` — público, informa se o primeiro cadastro já ocorreu;
- `POST /api/auth/setup` — público somente enquanto não existe administrador;
- `POST /api/auth/login` — público e protegido contra força bruta;
- `GET /api/auth/me` — exige JWT válido.

Todas as demais rotas `/api/*` exigem JWT do administrador ou `X-API-Key`. O webhook recebido do Chatwoot mantém a exceção necessária para chamadas externas.

### JWT e variáveis de ambiente

- `WACALLS_JWT_SECRET`: segredo opcional com pelo menos 32 caracteres. Quando não definido, o servidor gera um segredo criptograficamente aleatório e o persiste na tabela `app_settings` do banco principal;
- `WACALLS_JWT_TTL_HOURS`: duração do token em horas. O padrão é `12`, com limite entre `1` e `168` horas;
- `WACALLS_API_KEY`: credencial técnica opcional para integrações externas. Ela não aparece mais no formulário do administrador.

Em produção, publique o painel somente por HTTPS.

### Recuperação do administrador

Não existe rota pública de recuperação ou criação de um segundo usuário. Caso a senha seja perdida, acesse diretamente o banco principal e remova o registro único:

```sql
DELETE FROM admin_user WHERE id = 1;
```

Depois disso, o painel volta ao fluxo de primeiro acesso. Faça essa operação somente com acesso administrativo ao PostgreSQL e evite deixar o painel publicamente exposto durante o novo cadastro.

## Desenvolvimento

Com o servidor Go rodando na porta `8080`:

```bash
cd manager-v2
npm install
npm run dev
```

O Vite inicia em `http://localhost:5174` e encaminha `/api` para `http://localhost:8080`.

Também é possível informar outra URL diretamente na tela de autenticação ou no cabeçalho do painel.

## Build

```bash
cd manager-v2
npm install
npm run build
```

A saída é gerada em `manager-v2/dist`.

Para validar somente o backend usado pelo painel:

```bash
go build ./cmd/server
```

## Sessões e eventos

- `GET /api/sessions`
- `POST /api/sessions`
- `POST /api/sessions/{sid}/pair`
- `POST /api/sessions/{sid}/logout`
- `DELETE /api/sessions/{sid}`
- `GET /api/events`

## Chamadas

- `POST /api/sessions/{sid}/calls`
- `GET /api/sessions/{sid}/history`

## Mensagens comuns e mídia

- `POST /api/sessions/{sid}/messages/text`
- `POST /api/sessions/{sid}/messages/image`
- `POST /api/sessions/{sid}/messages/audio`
- `POST /api/sessions/{sid}/messages/video`
- `POST /api/sessions/{sid}/messages/document`
- `POST /api/sessions/{sid}/messages/sticker`

O endpoint de sticker aceita URL ou base64, mas o arquivo precisa estar no formato WebP (`image/webp`).

## Mensagens interativas

- `POST /api/sessions/{sid}/messages/button`
- `POST /api/sessions/{sid}/messages/list`
- `POST /api/sessions/{sid}/messages/location`
- `POST /api/sessions/{sid}/messages/contact`
- `POST /api/sessions/{sid}/messages/poll`
- `POST /api/sessions/{sid}/messages/reaction`

Botões de resposta aceitam no máximo três opções e não podem ser misturados com botões CTA. Os CTAs suportados são copiar, abrir URL e realizar chamada.

Para reagir a uma mensagem, informe o chat, o `messageId`, o emoji e o valor correto de `fromMe`. Em grupos, pode ser necessário informar também o participante.

## Integrações

- `GET /api/sessions/{sid}/webhook`
- `POST /api/sessions/{sid}/webhook`
- `DELETE /api/sessions/{sid}/webhook`

## Origem da implementação

O AstraCalls continua sendo a base do projeto por conter o núcleo de chamadas e o gerenciamento de sessões. Os recursos adicionais foram implementados diretamente sobre o `whatsmeow`, sem incorporar o frontend, o sistema de ativação ou o licenciamento do Evolution Go.
