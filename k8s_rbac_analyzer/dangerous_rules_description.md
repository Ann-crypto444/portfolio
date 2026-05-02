# Опасные правила RBAC: критерии и обоснование

Этот документ описывает критерии, по которым анализатор помечает RBAC‑права как потенциально опасные.

## Что считается «опасным»

Под опасным правилом понимается набор прав, который может быть использован для:

- **повышения привилегий** (privilege escalation) — получения прав выше исходных;
- **закрепления** (persistence) — добавления себе/другим учётным записям новых прав;
- **доступа к сенситивным данным** (например, Secrets, токены ServiceAccount);
- **разрушительных действий** (удаление/изменение критичных объектов, влияние на control plane).

Анализатор использует список детекторов из файла `dangerous_rules.yaml`. Каждый детектор описывает опасный «примитив» (например, чтение Secrets или управление admission webhooks).

## Как устроена проверка

1. Для каждого субъекта (User/Group/ServiceAccount) собирается **полный набор правил** из привязанных ролей.
2. Для каждого правила вычисляется **область действия**:
   - `CLUSTER` — если право получено через `ClusterRoleBinding`;
   - `NAMESPACE <ns>` — если право получено через `RoleBinding` в namespace `<ns>`.
3. Далее правило проверяется на пересечение с каждым детектором:
   - `apiGroups`, `resources`, `verbs` (и опционально `resourceNames`, `nonResourceURLs`) сравниваются по принципу **пересечения**;
   - в детекторах допускаются glob‑паттерны (например, `pods/*`).

### Как обрабатывается `*`

Наличие `*` в `resources` или `verbs` **не делает правило опасным автоматически**.

- Правило помечается опасным только если оно **пересекается** с одним из опасных примитивов.
- Для снижения ложных срабатываний есть `wildcard_allowlist`: если правило с `*` совпало с allowlist‑паттерном (например, чтение метрик в `metrics.k8s.io`), оно принудительно считается **неопасным**.

## Список детекторов (по умолчанию)

Ниже перечислены детекторы из `dangerous_rules.yaml` (ID → что именно считается опасным).

### K8S-RBAC-001 — Чтение Secrets

**Опасно**: `get/list/watch` на `secrets`.

**Почему**: Secrets содержат токены ServiceAccount, пароли, ключи и другие сенситивные данные. `list/watch` также раскрывают содержимое.

**Пример**:

- apiGroups: `[""]`
- resources: `["secrets"]`
- verbs: `["get","list","watch"]`

### K8S-RBAC-002 — Создание/изменение workload’ов

**Опасно**: `create/patch/update/delete` на `pods` и контроллеры (`deployments`, `statefulsets`, `daemonsets`, `replicasets`, `jobs`, `cronjobs`).

**Почему**: управление workload’ами часто эквивалентно доступу к данным приложения и может приводить к эскалации (например, запуск pod с монтированием Secret или использование более привилегированного ServiceAccount в namespace).

### K8S-RBAC-003 — Exec/Attach/PortForward/EphemeralContainers

**Опасно**: доступ к subresource `pods/exec`, `pods/attach`, `pods/portforward`, `pods/ephemeralcontainers`.

**Почему**: это интерактивный доступ к контейнеру (выполнение команд/подключение), который может привести к чтению секретов из томов/переменных окружения и дальнейшей эскалации.

### K8S-RBAC-004 — TokenRequest для ServiceAccount

**Опасно**: `create` на `serviceaccounts/token`.

**Почему**: позволяет выпускать токены для существующих ServiceAccount и использовать их права.

### K8S-RBAC-005 / K8S-RBAC-006 — CSR и Approval

**Опасно**:

- `create` на `certificatesigningrequests`;
- `update/patch/approve` на `certificatesigningrequests/approval`.

**Почему**: в зависимости от настроек signer’ов и цепочки доверия может приводить к выпуску клиентских сертификатов и эскалации.

### K8S-RBAC-007 — `escalate` на roles/clusterroles

**Опасно**: verb `escalate` для `roles`/`clusterroles`.

**Почему**: позволяет создавать/обновлять роли с правами выше собственных (обход встроенных ограничений).

### K8S-RBAC-008 — `bind` на roles/clusterroles

**Опасно**: verb `bind` для `roles`/`clusterroles`.

**Почему**: позволяет создавать биндинги к ролям с правами, которых у субъекта нет (типичный путь к эскалации).

### K8S-RBAC-009 — `impersonate`

**Опасно**: verb `impersonate` на `users/groups/serviceaccounts`.

**Почему**: позволяет действовать от имени другого субъекта.

### K8S-RBAC-010 — Управление admission webhooks

**Опасно**: изменение `mutatingwebhookconfigurations`/`validatingwebhookconfigurations`.

**Почему**: webhooks могут читать/мутировать объекты при admission, в том числе сенситивные данные.

### K8S-RBAC-011 — `nodes/proxy`

**Опасно**: `get` на `nodes/proxy`.

**Почему**: даёт доступ к Kubelet API; в ряде сценариев это может быть использовано для действий, которые обходят часть механизмов контроля.

### K8S-RBAC-012 — Создание PersistentVolume

**Опасно**: `create/update/patch/delete` на `persistentvolumes`.

**Почему**: есть риск hostPath‑PV и доступа к файловой системе узла.

### K8S-RBAC-013 — Изменение Namespace

**Опасно**: `patch/update` `namespaces`.

**Почему**: потенциально может ослабить политики (например, Pod Security Admission) или повлиять на label‑based ограничения.

### K8S-RBAC-014 — Изменение RBAC‑объектов

**Опасно**: запись в `roles/rolebindings/clusterroles/clusterrolebindings`.

**Почему**: прямой путь к эскалации или закреплению (добавление себе прав).

### K8S-RBAC-015 — `nonResourceURLs: ["*"]`

**Опасно**: wildcard доступ к non‑resource endpoint’ам.

**Почему**: может открыть доступ к чувствительным endpoint’ам (в зависимости от конфигурации кластера).

## Источники

Основной источник критериев — официальная документация Kubernetes:

- kubernetes.io → RBAC good practices
- kubernetes.io → RBAC reference (ограничения на create/update roles и bindings)

Ссылки на конкретные разделы приведены в `dangerous_rules.yaml` в поле `references` у каждого детектора.
