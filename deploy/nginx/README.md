# Diver Street Food — nginx vhost'ları

Bu dizin, `srv.diverstreetfood.com` sunucusunda 80/443 portlarını elinde tutan
**b2b projesinin** nginx konteynerine (`b2b_nginx`) dışarıdan mount edilen
vhost dosyalarını barındırır. Onlinemenu projesinin kendi nginx'i yoktur;
`docker-compose.prod.yml` / `docker-compose.diverserver.yml` içinde nginx
servisi tanımlı değildir.

## Nasıl devreye giriyor

1. `diverserver.conf` yalnızca `server{}` blokları içerir — `events{}`/`http{}`
   b2b'nin `nginx/nginx.conf` (veya SSL etkinken `nginx.conf.ssl`) dosyasında
   tanımlıdır.
2. b2b tarafında `nginx.conf` http{} bloğunun sonuna eklenen
   `include /etc/nginx/onlinemenu/*.conf;` satırı bu dizini okur.
3. b2b'nin `docker-compose.prod.yml` nginx servisine
   `/opt/onlinemenu/deploy/nginx:/etc/nginx/onlinemenu:ro` mount'u eklenmiştir.
   Sunucuda bu dizinin var olması için Online Menu reposu `/opt/onlinemenu`
   altında rsync ile durmalıdır (bkz. `docs/deployment.md` §5).
4. Servis keşfi Docker'ın gömülü DNS'i (`127.0.0.11`) + nginx `resolver` +
   değişkenli `proxy_pass` ile yapılır (`set $upstream ...; proxy_pass
   $upstream;`). Sabit `proxy_pass http://onlinemenu-admin:3000` **kullanılmaz**:
   nginx başlarken bu adresi hemen çözmeye çalışır, Online Menu konteynerleri
   henüz ayakta değilse çözüm başarısız olur ve b2b_nginx (dolayısıyla b2b'nin
   kendisi de) ayağa kalkamaz. Değişken + resolver çözümü istek anına erteler.

## Sertifika alma

Tek sertifika, dört SAN (`pos`/`menu`/`api`/`auth.diverstreetfood.com`),
`--cert-name` ile dizin adı `pos.diverstreetfood.com` olarak sabitlenir:

```bash
docker run --rm --network host \
  -v /etc/letsencrypt:/etc/letsencrypt \
  -v /var/www/certbot:/var/www/certbot \
  certbot/certbot certonly --webroot -w /var/www/certbot \
  --cert-name pos.diverstreetfood.com \
  -d pos.diverstreetfood.com \
  -d menu.diverstreetfood.com \
  -d api.diverstreetfood.com \
  -d auth.diverstreetfood.com \
  --email admin@diverstreetfood.com --agree-tos --no-eff-email
```

`--network host` zorunludur (b2b'nin `scripts/renew-ssl.sh` runbook dersi:
varsayılan bridge ağında DNS çözümü UFW/systemd-resolved yüzünden başarısız
olur). Yenileme ek işlem istemez: b2b'nin `renew-ssl.sh`'i `live/*/fullchain.pem`
glob'u ile çalıştığı için bu sertifika da otomatik kapsanır.

## Reload

Bu dizindeki dosyalarda değişiklik yaptıktan sonra (mount yolu **değişmediyse**):

```bash
docker exec b2b_nginx nginx -t && docker exec b2b_nginx nginx -s reload
```

Mount tanımının kendisi değiştiyse (örn. yol adı) b2b nginx servisi yeniden
yaratılmalıdır:

```bash
docker compose -f docker-compose.prod.yml --env-file .env.prod up -d nginx
```

(b2b reposu üzerinde, `/opt/b2b`.)

## Yeni alan adı eklerken

1. Cenuta'da yeni A kaydını `77.92.144.75`'e aç, `dig` ile yayılmayı doğrula.
2. Bu dosyada 80 bloğunun `server_name` listesine ekle; 443 için yeni bir
   `server{}` bloğu ekle (mevcut bloklardan kopyala, `server_name` ve upstream
   alias/port'unu değiştir).
3. Sertifikaya yeni `-d` ekleyerek `certbot certonly --webroot --cert-name
   pos.diverstreetfood.com ... -d yeni.diverstreetfood.com ...` komutunu tekrar
   çalıştır (aynı `--cert-name`, sertifika güncellenir).
4. `docker exec b2b_nginx nginx -t && docker exec b2b_nginx nginx -s reload`.
