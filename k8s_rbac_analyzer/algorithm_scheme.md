# Алгоритмическая схема работы анализатора

Ниже приведена схема основных этапов.

```mermaid
flowchart TD
  A[Вход: YAML/JSON манифесты RBAC] --> B[Парсинг документов (multi-doc YAML тоже)]
  B --> C[Реестр Role/ClusterRole]
  B --> D[Список RoleBinding/ClusterRoleBinding]

  C --> E[Нормализация правил роли]
  D --> F[Разворачивание биндингов: roleRef -> role.rules]
  F --> G[Эффективные права субъектов
(User/Group/ServiceAccount) + scope]

  G --> H[Движок детекторов опасности
(dangerous_rules.yaml)]
  H --> I[Отчёт: text/json
(все права или только опасные)]

  W[Опционально: YAML/JSON workload'ов] --> X[Извлечь serviceAccountName]
  X --> Y[Сопоставить SA -> workload'ы]
  Y --> I
```
