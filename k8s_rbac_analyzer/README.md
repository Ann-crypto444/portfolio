# k8s_rbac_analyzer

Статический анализ RBAC в Kubernetes:

- парсит `Role`, `ClusterRole`, `RoleBinding`, `ClusterRoleBinding` из YAML/JSON
- для каждого субъекта (User/Group/ServiceAccount) строит полный список доступных прав
- помечает права как `CLUSTER` (глобальные) или `NAMESPACE` (ограниченные namespace)
- выявляет потенциально опасные права по конфигу `dangerous_rules.yaml`
- умеет выводить все права или только опасные
- (опционально) показывает workload’ы, использующие ServiceAccount с опасными правами

## Быстрый запуск

```bash
python3 rbac_audit.py -i ./manifests/rbac -c dangerous_rules.yaml --format text
```

Только опасные:

```bash
python3 rbac_audit.py -i ./manifests/rbac --only-dangerous
```

JSON-отчёт:

```bash
python3 rbac_audit.py -i ./manifests/rbac --format json -o report.json
```

Звёздочка: нагрузка (workloads) → ServiceAccount:

```bash
python3 rbac_audit.py -i ./manifests/rbac --workloads ./manifests/workloads --only-dangerous
```

## Как устроен анализ

1. Парсим RBAC-объекты.
2. Разворачиваем биндинги:
   - `ClusterRoleBinding` → права `CLUSTER`
   - `RoleBinding` → права `NAMESPACE` (namespace = namespace биндинга)
3. Для каждого subject собираем union правил (с сохранением источников: binding → role).
4. Прогоняем каждое правило через детекторы из `dangerous_rules.yaml`.

### Почему `*` не = «опасно»

`*` помечается как опасный только тогда, когда он пересекается с одним из выбранных опасных примитивов
(например, `secrets`, `pods/exec`, `escalate`, `nodes/proxy` и т.д.) и с нужным `apiGroup`.

Дополнительно есть allowlist (`wildcard_allowlist`) в конфиге — чтобы подавлять известные безопасные wildcard-паттерны.

## Формат конфигурации dangerous_rules.yaml

См. комментарии в файле. Вы можете добавлять собственные детекторы в `dangerous_rules`.

## Ограничения

- Это статический анализ манифестов: без обращения к реальному API Kubernetes.
- В случае агрегации ClusterRole через `aggregationRule` реализована базовая поддержка по label selector’ам.
- Анализ workload’ов без доступа к кластеру показывает **манифесты**, а не реальные Pod’ы (которые создаст контроллер).

## Содержимое проекта

- `rbac_audit.py` — основной скрипт анализатора
- `dangerous_rules.yaml` — конфигурация опасных примитивов (расширяется без правок кода)
- `dangerous_rules_description.md` — текстовое описание критериев опасности
- `algorithm_scheme.md` / `algorithm_scheme.png` — алгоритмическая схема
- `requirements.txt` — зависимости Python
- `examples/` — тестовые данные и примеры результатов

