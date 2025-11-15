# 🚧 MODE DÉVELOPPEMENT - KSMTP

## Pourquoi le mode DEV ?

En développement, tu ne veux pas :
- ❌ Configurer DNS/SPF/DKIM sur ta machine locale
- ❌ Avoir un VPS qui tourne
- ❌ Rebuild et redéployer à chaque test
- ❌ Risquer d'envoyer de vrais emails par accident

Le **mode DEV** te permet de tester ton serveur SMTP **localement** en redirigeant tous les emails vers un serveur SMTP de développement (comme MailHog).

---

## 📦 Installation de MailHog

MailHog est un faux serveur SMTP qui capture tous les emails sans les envoyer.

### macOS
```bash
brew install mailhog
mailhog
```

### Linux
```bash
# Télécharger le binaire
wget https://github.com/mailhog/MailHog/releases/download/v1.0.1/MailHog_linux_amd64
chmod +x MailHog_linux_amd64
./MailHog_linux_amd64
```

### Windows
```bash
# Télécharger depuis: https://github.com/mailhog/MailHog/releases
# Lancer l'executable
```

### Docker (toutes plateformes)
```bash
docker run -d -p 1025:1025 -p 8025:8025 mailhog/mailhog
```

**Ports MailHog :**
- `1025` : SMTP (pour envoyer)
- `8025` : Interface web (pour voir les emails)

---

## 🚀 Utilisation du mode DEV

### 1️⃣ Activer le mode DEV dans ta config

```go
conf := &ksmtp.ConfServer{
    Domain:    "localhost",
    Subdomain: "mail.localhost",
    IPv4:      "127.0.0.1",
    Address:   ":2525",
    
    // *** ACTIVER LE MODE DEV ***
    DevMode:      true,              // Active le mode développement
    DevRelayHost: "localhost",        // Host du serveur SMTP de dev
    DevRelayPort: "1025",             // Port du serveur SMTP de dev (MailHog = 1025)
}

server, _ := ksmtp.NewSmtpServer(conf)
```

### 2️⃣ Envoyer des emails normalement

```go
// Envoie un email (sera capturé par MailHog)
email := &ksmtp.Email{
    From:    "test@localhost",
    To:      "user@gmail.com",      // N'importe quelle adresse !
    Subject: "Test",
    BodyTXT: "Hello World!",
}

server.SendEmail(email)
```

### 3️⃣ Voir les emails dans MailHog

Ouvre ton navigateur : **http://localhost:8025**

Tu verras tous les emails avec :
- ✅ Headers complets
- ✅ Signature DKIM
- ✅ Contenu HTML/Text
- ✅ Pièces jointes
- ✅ Structure MIME

---

## 🎯 Exemple complet

```go
package main

import (
    "log"
    "yourproject/ksmtp"
)

func main() {
    // Config avec mode DEV
    conf := &ksmtp.ConfServer{
        Domain:       "localhost",
        Subdomain:    "mail.localhost",
        IPv4:         "127.0.0.1",
        Address:      ":2525",
        DevMode:      true,
        DevRelayHost: "localhost",
        DevRelayPort: "1025",
    }
    
    server, err := ksmtp.NewSmtpServer(conf)
    if err != nil {
        log.Fatal(err)
    }
    
    // Lance le serveur
    go server.Start()
    
    // Envoie un email
    email := &ksmtp.Email{
        From:    "admin@localhost",
        To:      "user@example.com",
        Subject: "Test Email",
        BodyTXT: "Hello from KSMTP!",
    }
    
    err = server.SendEmail(email)
    if err != nil {
        log.Println("Erreur:", err)
    }
    
    // Vérifie http://localhost:8025
    select {} // Keep alive
}
```

---

## 🔄 Différences DEV vs PROD

| Fonctionnalité | Mode PROD | Mode DEV |
|----------------|-----------|----------|
| MX Lookup | ✅ Oui | ❌ Non (bypass) |
| Connexion SMTP | Serveurs réels (Gmail, etc.) | Serveur local (MailHog) |
| DKIM | ✅ Signé | ✅ Signé (visible dans MailHog) |
| DNS requis | ✅ Oui (SPF, DKIM, PTR) | ❌ Non |
| VPS requis | ✅ Oui | ❌ Non |
| Emails envoyés | ✅ Oui (vrais emails) | ❌ Non (capturés localement) |

---

## 🛠️ Autres serveurs SMTP de DEV

Tu peux utiliser d'autres serveurs que MailHog :

### Mailtrap
```go
DevMode:      true,
DevRelayHost: "smtp.mailtrap.io",
DevRelayPort: "2525",
// Nécessite aussi: username/password
```

### Mailcatcher
```bash
gem install mailcatcher
mailcatcher
```
```go
DevMode:      true,
DevRelayHost: "localhost",
DevRelayPort: "1025",
```

### Papercut (Windows)
- Télécharger : https://github.com/ChangemakerStudios/Papercut-SMTP
```go
DevMode:      true,
DevRelayHost: "localhost",
DevRelayPort: "25",
```

---

## ⚡ Workflow recommandé

### Développement Local
```go
DevMode: true,
DevRelayHost: "localhost",
DevRelayPort: "1025",
```
👉 Teste rapidement sur ta machine

### Staging/Test
```go
DevMode: true,
DevRelayHost: "smtp.mailtrap.io",
DevRelayPort: "2525",
```
👉 Teste avec une vraie infra mais sans envoyer de vrais emails

### Production
```go
DevMode: false,
// DevRelayHost et DevRelayPort sont ignorés
```
👉 Envoie de vrais emails via MX lookup

---

## 🚨 Sécurité

⚠️ **IMPORTANT** : Ne jamais activer `DevMode: true` en production !

Le mode DEV :
- Bypass les vérifications MX
- Peut exposer des données sensibles
- N'utilise pas la vraie infrastructure email

Assure-toi que `DevMode` est bien à `false` en production.

---

## ✅ Checklist

Avant de tester en local :

- [ ] MailHog installé et lancé (`mailhog`)
- [ ] Interface MailHog accessible (http://localhost:8025)
- [ ] `DevMode: true` dans ta config
- [ ] `DevRelayHost: "localhost"`
- [ ] `DevRelayPort: "1025"`
- [ ] Ton serveur KSMTP démarre sans erreur
- [ ] Tu envoies un email de test
- [ ] L'email apparaît dans MailHog

---

## 🎉 Avantages

✅ **Développement rapide** : Pas besoin de rebuild/deploy  
✅ **Zéro config** : Pas de DNS, DKIM, SPF à configurer  
✅ **Sécurité** : Aucun risque d'envoyer de vrais emails par erreur  
✅ **Debug facile** : Voir tous les headers, DKIM, MIME, etc.  
✅ **Offline** : Fonctionne sans connexion internet  
✅ **Tests automatisés** : Parfait pour les tests unitaires  

---

## 📝 Notes

- Les emails envoyés en mode DEV sont **capturés localement** et ne sont jamais envoyés aux vrais destinataires
- La signature DKIM est quand même appliquée (tu peux la voir dans MailHog)
- Tous les templates, pièces jointes, HTML/Text fonctionnent normalement
- Le mode DEV est **thread-safe** et supporte les envois concurrents

---

## 🆘 Problèmes courants

### "failed to connect to dev relay"
➡️ MailHog n'est pas lancé. Lance `mailhog` dans un terminal.

### "connection refused on port 1025"
➡️ Vérifie que MailHog écoute bien sur le port 1025 (par défaut).

### Les emails n'apparaissent pas dans MailHog
➡️ Vérifie la console de ton app pour les erreurs.
➡️ Vérifie que `DevMode: true` est bien activé.

---

**Bon développement ! 🚀**

