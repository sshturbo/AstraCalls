# AstraCalls Manager v2

Painel técnico para administrar sessões e testar a API do AstraCalls.

## Escopo

O Manager v2 não é uma caixa de entrada e não mantém conversas. Ele serve para:

- configurar a URL da API e a API key;
- criar, selecionar, parear, desconectar e excluir sessões;
- exibir QR code e estado da conexão em tempo real via SSE;
- testar chamadas, texto, imagem, áudio, vídeo, documento e webhook;
- visualizar status HTTP, duração e JSON de resposta;
- acompanhar eventos e erros para depuração;
- mostrar os endpoints planejados sem apresentá-los como disponíveis.

## Desenvolvimento

Com o servidor Go rodando na porta `8080`:

```bash
cd manager-v2
npm install
npm run dev
```

O Vite inicia em `http://localhost:5174` e encaminha `/api` para `http://localhost:8080`.

Também é possível informar outra URL diretamente no cabeçalho do painel.

## Build

```bash
cd manager-v2
npm install
npm run build
```

A saída é gerada em `manager-v2/dist`.

## Endpoints atuais conectados

- `GET/POST /api/sessions`
- `POST /api/sessions/{sid}/pair`
- `POST /api/sessions/{sid}/logout`
- `DELETE /api/sessions/{sid}`
- `GET /api/events`
- `POST /api/sessions/{sid}/calls`
- `GET /api/sessions/{sid}/history`
- `POST /api/sessions/{sid}/messages/text`
- `POST /api/sessions/{sid}/messages/image`
- `POST /api/sessions/{sid}/messages/audio`
- `POST /api/sessions/{sid}/messages/video`
- `POST /api/sessions/{sid}/messages/document`
- `GET/POST /api/sessions/{sid}/webhook`

## Próxima etapa do backend

Adicionar endpoints próprios no AstraCalls para botões, listas, localização, contatos, enquete, reações, stickers e demais formatos suportados pelo `whatsmeow`. A implementação deve ser nativa no projeto, sem importar o sistema de licença ou o frontend do Evolution Go.
