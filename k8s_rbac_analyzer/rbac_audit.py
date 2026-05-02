from __future__ import annotations

import argparse
import fnmatch
import json
import os
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Sequence, Set, Tuple

import yaml


def _as_tuple_str(value: Any) -> Tuple[str, ...]:
    if value is None:
        return tuple()
    if isinstance(value, (list, tuple, set)):
        return tuple(str(x) for x in value)
    return (str(value),)


@dataclass(frozen=True)
class PolicyRule:
    api_groups: Tuple[str, ...] = ()
    resources: Tuple[str, ...] = ()
    verbs: Tuple[str, ...] = ()
    resource_names: Tuple[str, ...] = ()
    non_resource_urls: Tuple[str, ...] = ()

    @staticmethod
    def from_dict(d: Dict[str, Any]) -> "PolicyRule":
        return PolicyRule(
            api_groups=_as_tuple_str(d.get("apiGroups")),
            resources=_as_tuple_str(d.get("resources")),
            verbs=_as_tuple_str(d.get("verbs")),
            resource_names=_as_tuple_str(d.get("resourceNames")),
            non_resource_urls=_as_tuple_str(d.get("nonResourceURLs")),
        )

    def is_non_resource(self) -> bool:
        return bool(self.non_resource_urls)

    def normalized(self) -> "PolicyRule":
        # сортировка для стабильного хеша/сравнения
        return PolicyRule(
            api_groups=tuple(sorted(set(self.api_groups))),
            resources=tuple(sorted(set(self.resources))),
            verbs=tuple(sorted(set(self.verbs))),
            resource_names=tuple(sorted(set(self.resource_names))),
            non_resource_urls=tuple(sorted(set(self.non_resource_urls))),
        )


@dataclass
class RoleObj:
    kind: str  # Role / ClusterRole
    name: str
    namespace: Optional[str]
    rules: List[PolicyRule] = field(default_factory=list)
    labels: Dict[str, str] = field(default_factory=dict)
    aggregation_rule: Optional[Dict[str, Any]] = None
    source: Optional[str] = None  # файл, из которого загружено

    @property
    def key(self) -> Tuple[str, str, str]:
        # (kind, namespace, name) where namespace is "" for cluster-scoped
        return (self.kind, self.namespace or "", self.name)


@dataclass(frozen=True)
class Subject:
    kind: str  # User / Group / ServiceAccount
    name: str
    namespace: Optional[str] = None

    @property
    def key(self) -> Tuple[str, str, str]:
        return (self.kind, self.namespace or "", self.name)

    def display(self) -> str:
        if self.kind.lower() == "serviceaccount":
            ns = self.namespace or "?"
            return f"ServiceAccount {ns}:{self.name}"
        return f"{self.kind} {self.name}"


@dataclass
class BindingObj:
    kind: str  # RoleBinding / ClusterRoleBinding
    name: str
    namespace: Optional[str]
    role_ref_kind: str
    role_ref_name: str
    role_ref_api_group: Optional[str]
    subjects: List[Subject] = field(default_factory=list)
    source: Optional[str] = None


@dataclass
class SourceRef:
    binding_kind: str
    binding_name: str
    binding_namespace: Optional[str]
    role_kind: str
    role_name: str
    role_namespace: Optional[str]

    def to_dict(self) -> Dict[str, Any]:
        return {
            "binding": {
                "kind": self.binding_kind,
                "name": self.binding_name,
                "namespace": self.binding_namespace,
            },
            "role": {
                "kind": self.role_kind,
                "name": self.role_name,
                "namespace": self.role_namespace,
            },
        }


@dataclass
class DangerousMatch:
    rule_id: str
    title: str
    severity: str
    rationale: str = ""

    def to_dict(self) -> Dict[str, Any]:
        return {
            "id": self.rule_id,
            "title": self.title,
            "severity": self.severity,
            "rationale": self.rationale,
        }


@dataclass
class PermissionEntry:
    scope: str  # "cluster" | "namespace"
    namespace: Optional[str]
    rule: PolicyRule
    sources: List[SourceRef] = field(default_factory=list)
    dangerous: bool = False
    matches: List[DangerousMatch] = field(default_factory=list)

    def key(self) -> Tuple[str, str, PolicyRule]:
        return (self.scope, self.namespace or "", self.rule.normalized())


@dataclass
class DangerousRuleDef:
    rule_id: str
    title: str
    severity: str
    applies_to: str  
    rationale: str = ""
    references: List[str] = field(default_factory=list)
    match: Dict[str, Any] = field(default_factory=dict)

    @staticmethod
    def from_dict(d: Dict[str, Any]) -> "DangerousRuleDef":
        return DangerousRuleDef(
            rule_id=str(d.get("id") or d.get("rule_id") or ""),
            title=str(d.get("title") or ""),
            severity=str(d.get("severity") or "high"),
            applies_to=str(d.get("applies_to") or d.get("scope") or "any"),
            rationale=str(d.get("rationale") or d.get("description") or ""),
            references=list(d.get("references") or []),
            match=dict(d.get("match") or {}),
        )

    def applies_to_scope(self, scope: str) -> bool:
        if self.applies_to in ("any", "*", ""):
            return True
        return self.applies_to == scope

    def matches_rule(self, rule: PolicyRule) -> bool:
        m = self.match or {}

        # nonResourceURLs
        if "nonResourceURLs" in m or "non_resource_urls" in m:
            patterns = _as_tuple_str(m.get("nonResourceURLs") or m.get("non_resource_urls"))
            if not rule.non_resource_urls:
                return False
            return _overlap_with_globs(rule.non_resource_urls, patterns)

        patterns_api = _as_tuple_str(m.get("apiGroups") or m.get("api_groups") or ("*",))
        patterns_res = _as_tuple_str(m.get("resources") or ("*",))
        patterns_verbs = _as_tuple_str(m.get("verbs") or ("*",))
        patterns_rnames = _as_tuple_str(m.get("resourceNames") or m.get("resource_names") or ())

        if not rule.resources:
            return False

        if not _overlap_with_globs(rule.api_groups or ("",), patterns_api):
            return False
        if not _overlap_with_globs(rule.resources, patterns_res):
            return False
        if not _overlap_with_globs(rule.verbs, patterns_verbs):
            return False

        if patterns_rnames:
            # если шаблон требует конкретные resourceNames, то должно пересечься
            if not rule.resource_names:
                return False
            if not _overlap_with_globs(rule.resource_names, patterns_rnames):
                return False

        return True


def _overlap_with_globs(values: Sequence[str], patterns: Sequence[str]) -> bool:
    if not values or not patterns:
        return False

    values_set = set(values)
    patterns_set = set(patterns)

    if "*" in values_set:
        return True
    if "*" in patterns_set:
        return True

    for v in values:
        for p in patterns:
            if fnmatch.fnmatch(v, p):
                return True
            if fnmatch.fnmatch(p, v):
                return True
    return False


@dataclass
class AnalyzerConfig:
    dangerous_rules: List[DangerousRuleDef] = field(default_factory=list)
    cluster_scoped_resources: Set[Tuple[str, str]] = field(default_factory=set)
    wildcard_allowlist: List[Dict[str, Any]] = field(default_factory=list)

    @staticmethod
    def load(path: Path) -> "AnalyzerConfig":
        raw = yaml.safe_load(path.read_text(encoding="utf-8"))
        if not isinstance(raw, dict):
            raise ValueError(f"Config {path} must be a YAML mapping at top-level")

        cr = set()
        for item in raw.get("cluster_scoped_resources", []) or []:
            if isinstance(item, dict):
                ag = str(item.get("apiGroup", item.get("api_group", "")))
                res = str(item.get("resource", ""))
                if res:
                    cr.add((ag, res))
            elif isinstance(item, str):
                if "/" in item:
                    ag, res = item.split("/", 1)
                    cr.add((ag, res))
                else:
                    cr.add(("", item))

        d_rules = []
        for rd in raw.get("dangerous_rules", []) or []:
            if not isinstance(rd, dict):
                continue
            d_rules.append(DangerousRuleDef.from_dict(rd))

        wl = list(raw.get("wildcard_allowlist", []) or [])

        return AnalyzerConfig(
            dangerous_rules=d_rules,
            cluster_scoped_resources=cr,
            wildcard_allowlist=wl,
        )

RBAC_KINDS = {"Role", "ClusterRole", "RoleBinding", "ClusterRoleBinding"}

WORKLOAD_KINDS = {
    "Pod",
    "Deployment",
    "ReplicaSet",
    "StatefulSet",
    "DaemonSet",
    "Job",
    "CronJob",
}


def discover_files(paths: List[Path]) -> List[Path]:
    out: List[Path] = []
    for p in paths:
        if p.is_file():
            out.append(p)
        elif p.is_dir():
            for root, _, files in os.walk(p):
                for f in files:
                    if f.lower().endswith((".yaml", ".yml", ".json")):
                        out.append(Path(root) / f)
    # стабильно
    return sorted(set(out))


def load_documents(file_path: Path) -> List[Dict[str, Any]]:
    txt = file_path.read_text(encoding="utf-8")
    if file_path.suffix.lower() == ".json":
        data = json.loads(txt)
        if isinstance(data, list):
            return [x for x in data if isinstance(x, dict)]
        if isinstance(data, dict):
            return [data]
        return []

    docs = []
    for doc in yaml.safe_load_all(txt):
        if isinstance(doc, dict):
            docs.append(doc)
    return docs


def parse_rbac_objects(files: List[Path]) -> Tuple[Dict[Tuple[str, str], RoleObj], Dict[str, RoleObj], List[BindingObj], List[str]]:
    roles: Dict[Tuple[str, str], RoleObj] = {}
    clusterroles: Dict[str, RoleObj] = {}
    bindings: List[BindingObj] = []
    warnings: List[str] = []

    for fp in files:
        try:
            docs = load_documents(fp)
        except Exception as e:
            warnings.append(f"[WARN] Cannot parse {fp}: {e}")
            continue

        for obj in docs:
            kind = str(obj.get("kind") or "")
            if kind not in RBAC_KINDS:
                continue

            md = obj.get("metadata") or {}
            name = str(md.get("name") or "")
            namespace = md.get("namespace")
            namespace = str(namespace) if namespace is not None else None

            if not name:
                warnings.append(f"[WARN] {fp}: object kind={kind} without metadata.name")
                continue

            if kind in ("Role", "ClusterRole"):
                rules_raw = obj.get("rules") or []
                rules: List[PolicyRule] = []
                for r in rules_raw:
                    if isinstance(r, dict):
                        rules.append(PolicyRule.from_dict(r).normalized())

                labels = md.get("labels") or {}
                if not isinstance(labels, dict):
                    labels = {}
                labels = {str(k): str(v) for k, v in labels.items()}

                agg = obj.get("aggregationRule")
                role_obj = RoleObj(
                    kind=kind,
                    name=name,
                    namespace=namespace if kind == "Role" else None,
                    rules=rules,
                    labels=labels,
                    aggregation_rule=agg if isinstance(agg, dict) else None,
                    source=str(fp),
                )

                if kind == "Role":
                    if not role_obj.namespace:
                        warnings.append(f"[WARN] {fp}: Role {name} without metadata.namespace (ignored)")
                        continue
                    roles[(role_obj.namespace, role_obj.name)] = role_obj
                else:
                    clusterroles[role_obj.name] = role_obj

            elif kind in ("RoleBinding", "ClusterRoleBinding"):
                rb_namespace = namespace if kind == "RoleBinding" else None

                rr = obj.get("roleRef") or {}
                rr_kind = str(rr.get("kind") or "")
                rr_name = str(rr.get("name") or "")
                rr_ag = rr.get("apiGroup")
                rr_ag = str(rr_ag) if rr_ag is not None else None

                if not rr_kind or not rr_name:
                    warnings.append(f"[WARN] {fp}: {kind} {name} without roleRef.kind/name")
                    continue

                subjects_raw = obj.get("subjects") or []
                subjects: List[Subject] = []
                if isinstance(subjects_raw, list):
                    for s in subjects_raw:
                        if not isinstance(s, dict):
                            continue
                        skind = str(s.get("kind") or "")
                        sname = str(s.get("name") or "")
                        sns = s.get("namespace")
                        sns = str(sns) if sns is not None else None

                        if not skind or not sname:
                            continue

                        if skind.lower() == "serviceaccount" and not sns:
                            sns = rb_namespace

                        subjects.append(Subject(kind=skind, name=sname, namespace=sns))

                bindings.append(
                    BindingObj(
                        kind=kind,
                        name=name,
                        namespace=rb_namespace,
                        role_ref_kind=rr_kind,
                        role_ref_name=rr_name,
                        role_ref_api_group=rr_ag,
                        subjects=subjects,
                        source=str(fp),
                    )
                )

    return roles, clusterroles, bindings, warnings

def _label_selector_matches(labels: Dict[str, str], selector: Dict[str, Any]) -> bool:
    """
    Минимальная реализация LabelSelector (matchLabels + matchExpressions).
    """
    if not isinstance(selector, dict):
        return False

    ml = selector.get("matchLabels") or {}
    if isinstance(ml, dict):
        for k, v in ml.items():
            if labels.get(str(k)) != str(v):
                return False

    me = selector.get("matchExpressions") or []
    if isinstance(me, list):
        for expr in me:
            if not isinstance(expr, dict):
                continue
            key = str(expr.get("key") or "")
            op = str(expr.get("operator") or "")
            values = expr.get("values") or []
            values = [str(x) for x in values] if isinstance(values, list) else []

            if op == "In":
                if labels.get(key) not in set(values):
                    return False
            elif op == "NotIn":
                if labels.get(key) in set(values):
                    return False
            elif op == "Exists":
                if key not in labels:
                    return False
            elif op == "DoesNotExist":
                if key in labels:
                    return False
            else:
                return False

    return True


def apply_clusterrole_aggregation(clusterroles: Dict[str, RoleObj], warnings: List[str]) -> None:
    all_roles = list(clusterroles.values())

    for cr in all_roles:
        agg = cr.aggregation_rule
        if not agg:
            continue

        selectors = agg.get("clusterRoleSelectors") or []
        if not isinstance(selectors, list):
            continue

        aggregated: List[PolicyRule] = []
        for sel in selectors:
            if not isinstance(sel, dict):
                continue
            for candidate in all_roles:
                if candidate.name == cr.name:
                    continue
                if _label_selector_matches(candidate.labels, sel):
                    aggregated.extend(candidate.rules)

        if aggregated:
            merged = {r.normalized() for r in (cr.rules + aggregated)}
            cr.rules = sorted(merged, key=lambda r: (",".join(r.api_groups), ",".join(r.resources), ",".join(r.verbs)))


@dataclass
class AnalysisResult:
    subjects: Dict[Tuple[str, str, str], Subject] = field(default_factory=dict)
    permissions: Dict[Tuple[str, str, str], Dict[Tuple[str, str, PolicyRule], PermissionEntry]] = field(default_factory=dict)
    roles_dangerous: Dict[Tuple[str, str, str], List[DangerousMatch]] = field(default_factory=dict)
    warnings: List[str] = field(default_factory=list)

    def subject_entries(self) -> List[Tuple[Subject, List[PermissionEntry]]]:
        out = []
        for skey, subj in sorted(self.subjects.items(), key=lambda x: (x[1].kind, x[1].namespace or "", x[1].name)):
            perms = list(self.permissions.get(skey, {}).values())
            perms.sort(key=lambda p: (p.scope, p.namespace or "", ",".join(p.rule.api_groups), ",".join(p.rule.resources), ",".join(p.rule.verbs)))
            out.append((subj, perms))
        return out


class RBACAnalyzer:
    def __init__(self, config: AnalyzerConfig) -> None:
        self.config = config

    def analyze(self, roles: Dict[Tuple[str, str], RoleObj], clusterroles: Dict[str, RoleObj], bindings: List[BindingObj], warnings: List[str]) -> AnalysisResult:
        res = AnalysisResult(warnings=list(warnings))

        self._mark_dangerous_roles(res, roles, clusterroles)

        for b in bindings:
            scope = "cluster" if b.kind == "ClusterRoleBinding" else "namespace"
            ns = None if scope == "cluster" else b.namespace

            role_obj = self._resolve_role_ref(b, roles, clusterroles, res.warnings)
            if not role_obj:
                continue

            effective_rules = self._effective_rules_for_binding(scope, role_obj.rules)

            for subj in b.subjects:
                # валидируем SA namespace
                subj_fixed = subj
                if subj.kind.lower() == "serviceaccount":
                    subj_fixed = Subject(kind=subj.kind, name=subj.name, namespace=subj.namespace or b.namespace)

                res.subjects[subj_fixed.key] = subj_fixed

                per_subject = res.permissions.setdefault(subj_fixed.key, {})

                for rule in effective_rules:
                    pe = PermissionEntry(scope=scope, namespace=ns, rule=rule.normalized())
                    pkey = pe.key()
                    if pkey not in per_subject:
                        per_subject[pkey] = pe
                    per_subject[pkey].sources.append(
                        SourceRef(
                            binding_kind=b.kind,
                            binding_name=b.name,
                            binding_namespace=b.namespace,
                            role_kind=role_obj.kind,
                            role_name=role_obj.name,
                            role_namespace=role_obj.namespace,
                        )
                    )

        # 3) Оценим опасность на уровне конкретных grant’ов
        self._evaluate_permissions(res)

        return res

    def _resolve_role_ref(self, b: BindingObj, roles: Dict[Tuple[str, str], RoleObj], clusterroles: Dict[str, RoleObj], warnings: List[str]) -> Optional[RoleObj]:
        rk = b.role_ref_kind
        rn = b.role_ref_name

        if rk == "Role":
            if not b.namespace:
                warnings.append(f"[WARN] {b.source}: RoleBinding {b.name} without namespace")
                return None
            role_obj = roles.get((b.namespace, rn))
            if not role_obj:
                warnings.append(f"[WARN] {b.source}: RoleBinding {b.name} refers to missing Role {b.namespace}/{rn}")
            return role_obj

        if rk == "ClusterRole":
            role_obj = clusterroles.get(rn)
            if not role_obj:
                warnings.append(f"[WARN] {b.source}: {b.kind} {b.name} refers to missing ClusterRole {rn}")
            return role_obj

        warnings.append(f"[WARN] {b.source}: {b.kind} {b.name} has unsupported roleRef.kind={rk}")
        return None

    def _effective_rules_for_binding(self, scope: str, rules: List[PolicyRule]) -> List[PolicyRule]:
        out: List[PolicyRule] = []
        for r in rules:
            if r.non_resource_urls and scope != "cluster":
                continue
            out.append(r)
        return out

    def _mark_dangerous_roles(self, res: AnalysisResult, roles: Dict[Tuple[str, str], RoleObj], clusterroles: Dict[str, RoleObj]) -> None:
        # ClusterRoles
        for cr in clusterroles.values():
            matches = self._matches_for_rule_list("cluster", cr.rules)
            if matches:
                res.roles_dangerous[cr.key] = matches

        # Roles (namespaced)
        for r in roles.values():
            matches = self._matches_for_rule_list("namespace", r.rules)
            if matches:
                res.roles_dangerous[r.key] = matches

    def _evaluate_permissions(self, res: AnalysisResult) -> None:
        for skey, pmap in res.permissions.items():
            for pkey, pe in pmap.items():
                matches = self._matches_for_policy_rule(pe.scope, pe.rule)
                # wildcard allowlist: если правило содержит '*' и полностью "разрешено", то не помечаем
                if matches and self._is_wildcard_allowlisted(pe.scope, pe.rule):
                    continue

                if matches:
                    pe.dangerous = True
                    pe.matches.extend(matches)

    def _matches_for_rule_list(self, scope: str, rules: List[PolicyRule]) -> List[DangerousMatch]:
        out: List[DangerousMatch] = []
        for r in rules:
            out.extend(self._matches_for_policy_rule(scope, r))
        # дедуп по id
        uniq: Dict[str, DangerousMatch] = {}
        for m in out:
            uniq[m.rule_id] = m
        return list(sorted(uniq.values(), key=lambda x: (x.severity, x.rule_id)))

    def _matches_for_policy_rule(self, scope: str, rule: PolicyRule) -> List[DangerousMatch]:
        out: List[DangerousMatch] = []
        for dr in self.config.dangerous_rules:
            if not dr.applies_to_scope(scope):
                continue
            if dr.matches_rule(rule):
                out.append(DangerousMatch(rule_id=dr.rule_id, title=dr.title, severity=dr.severity, rationale=dr.rationale))
        return out

    def _is_wildcard_allowlisted(self, scope: str, rule: PolicyRule) -> bool:
        has_star = "*" in set(rule.api_groups + rule.resources + rule.verbs + rule.non_resource_urls)
        if not has_star:
            return False

        for item in self.config.wildcard_allowlist:
            if not isinstance(item, dict):
                continue
            applies_to = str(item.get("applies_to") or item.get("scope") or "any")
            if applies_to not in ("any", "*", "", scope):
                continue

            m = item.get("match") or item
            if not isinstance(m, dict):
                continue

            # используем тот же механизм пересечения
            tmp = DangerousRuleDef(
                rule_id="ALLOWLIST",
                title="ALLOWLIST",
                severity="none",
                applies_to=applies_to,
                match=m,
            )
            if tmp.matches_rule(rule):
                return True

        return False

@dataclass(frozen=True)
class WorkloadRef:
    kind: str
    namespace: str
    name: str
    source: Optional[str] = None

    def display(self) -> str:
        src = f" ({self.source})" if self.source else ""
        return f"{self.kind} {self.namespace}/{self.name}{src}"


def parse_workloads(files: List[Path]) -> Tuple[Dict[Tuple[str, str, str], List[WorkloadRef]], List[str]]:
    """
    Возвращает mapping:
      (ServiceAccount, namespace, name) -> [WorkloadRef, ...]
    """
    mapping: Dict[Tuple[str, str, str], List[WorkloadRef]] = {}
    warnings: List[str] = []

    for fp in files:
        try:
            docs = load_documents(fp)
        except Exception as e:
            warnings.append(f"[WARN] Cannot parse workload file {fp}: {e}")
            continue

        for obj in docs:
            kind = str(obj.get("kind") or "")
            if kind not in WORKLOAD_KINDS:
                continue

            md = obj.get("metadata") or {}
            name = str(md.get("name") or "")
            if not name:
                continue
            ns = md.get("namespace")
            ns = str(ns) if ns is not None else "default"

            sa = extract_serviceaccount_name(obj)
            if not sa:
                sa = "default"

            key = ("ServiceAccount", ns, sa)
            mapping.setdefault(key, []).append(WorkloadRef(kind=kind, namespace=ns, name=name, source=str(fp)))

    return mapping, warnings


def extract_serviceaccount_name(obj: Dict[str, Any]) -> Optional[str]:
    kind = str(obj.get("kind") or "")

    def _get(d: Any, path: List[str]) -> Any:
        cur = d
        for p in path:
            if not isinstance(cur, dict):
                return None
            cur = cur.get(p)
        return cur

    if kind == "Pod":
        return _get(obj, ["spec", "serviceAccountName"]) or _get(obj, ["spec", "serviceAccount"])

    # большинство workload’ов: spec.template.spec.serviceAccountName
    if kind in {"Deployment", "ReplicaSet", "StatefulSet", "DaemonSet", "Job"}:
        return _get(obj, ["spec", "template", "spec", "serviceAccountName"]) or _get(obj, ["spec", "template", "spec", "serviceAccount"])

    if kind == "CronJob":
        return _get(obj, ["spec", "jobTemplate", "spec", "template", "spec", "serviceAccountName"]) or _get(obj, ["spec", "jobTemplate", "spec", "template", "spec", "serviceAccount"])

    return None


# Отчёты
def rule_to_compact_str(rule: PolicyRule) -> str:
    if rule.non_resource_urls:
        return f"nonResourceURLs={list(rule.non_resource_urls)} verbs={list(rule.verbs)}"
    parts = []
    parts.append(f"apiGroups={list(rule.api_groups) if rule.api_groups else ['']}")
    parts.append(f"resources={list(rule.resources)}")
    parts.append(f"verbs={list(rule.verbs)}")
    if rule.resource_names:
        parts.append(f"resourceNames={list(rule.resource_names)}")
    return " ".join(parts)


def render_text_report(result: AnalysisResult, only_dangerous: bool, include_sources: bool, include_role_danger: bool, workload_map: Optional[Dict[Tuple[str, str, str], List[WorkloadRef]]] = None) -> str:
    lines: List[str] = []

    # summary
    total_subjects = len(result.subjects)
    total_perms = sum(len(p) for p in result.permissions.values())
    dangerous_perms = sum(1 for pmap in result.permissions.values() for pe in pmap.values() if pe.dangerous)
    dangerous_subjects = sum(1 for skey, pmap in result.permissions.items() if any(pe.dangerous for pe in pmap.values()))

    lines.append("Kubernetes RBAC Analyzer report")
    lines.append(f"Subjects: {total_subjects}; Permissions: {total_perms}; Dangerous permissions: {dangerous_perms}; Subjects with dangerous permissions: {dangerous_subjects}")
    lines.append("")

    if result.warnings:
        lines.append("Warnings:")
        for w in result.warnings:
            lines.append(f"  - {w}")
        lines.append("")

    if include_role_danger and result.roles_dangerous:
        lines.append("Dangerous Roles/ClusterRoles (by content):")
        for rkey, matches in sorted(result.roles_dangerous.items(), key=lambda x: (x[0][0], x[0][1], x[0][2])):
            kind, ns, name = rkey
            ns_part = f"{ns}/" if ns else ""
            lines.append(f"  - {kind} {ns_part}{name}")
            for m in matches:
                lines.append(f"      * [{m.severity}] {m.rule_id}: {m.title}")
        lines.append("")

    for subj, perms in result.subject_entries():
        # filter perms
        if only_dangerous:
            perms = [p for p in perms if p.dangerous]

        if not perms:
            continue

        lines.append(subj.display())

        def _grp_key(p: PermissionEntry) -> Tuple[str, str]:
            return (p.scope, p.namespace or "")

        current_group: Optional[Tuple[str, str]] = None
        for p in perms:
            g = _grp_key(p)
            if g != current_group:
                current_group = g
                if p.scope == "cluster":
                    lines.append("  Scope: CLUSTER (all namespaces / cluster-wide)")
                else:
                    lines.append(f"  Scope: NAMESPACE {p.namespace}")

            danger_mark = "DANGEROUS" if p.dangerous else "ok"
            lines.append(f"    - [{danger_mark}] {rule_to_compact_str(p.rule)}")

            if p.dangerous and p.matches:
                for m in sorted(p.matches, key=lambda x: (x.severity, x.rule_id)):
                    lines.append(f"        -> [{m.severity}] {m.rule_id}: {m.title}")

            if include_sources and p.sources:
                for s in p.sources:
                    bns = f"{s.binding_namespace}/" if s.binding_namespace else ""
                    rns = f"{s.role_namespace}/" if s.role_namespace else ""
                    lines.append(f"        source: {s.binding_kind} {bns}{s.binding_name} -> {s.role_kind} {rns}{s.role_name}")

        if workload_map and subj.kind.lower() == "serviceaccount":
            wl = workload_map.get(subj.key, [])
            is_danger_sa = any(p.dangerous for p in result.permissions.get(subj.key, {}).values())
            if wl and (is_danger_sa or not only_dangerous):
                lines.append("  Workloads using this ServiceAccount:")
                for w in sorted(wl, key=lambda x: (x.namespace, x.kind, x.name)):
                    lines.append(f"    - {w.display()}")

        lines.append("")

    return "\n".join(lines).rstrip() + "\n"


def build_json_report(result: AnalysisResult, only_dangerous: bool, include_sources: bool, include_role_danger: bool, workload_map: Optional[Dict[Tuple[str, str, str], List[WorkloadRef]]] = None) -> Dict[str, Any]:
    out: Dict[str, Any] = {
        "summary": {
            "subjects": len(result.subjects),
            "permissions": sum(len(p) for p in result.permissions.values()),
            "dangerous_permissions": sum(1 for pmap in result.permissions.values() for pe in pmap.values() if pe.dangerous),
            "subjects_with_dangerous_permissions": sum(1 for skey, pmap in result.permissions.items() if any(pe.dangerous for pe in pmap.values())),
        },
        "warnings": list(result.warnings),
        "subjects": [],
    }

    if include_role_danger:
        roles = []
        for rkey, matches in sorted(result.roles_dangerous.items(), key=lambda x: (x[0][0], x[0][1], x[0][2])):
            kind, ns, name = rkey
            roles.append(
                {
                    "kind": kind,
                    "namespace": ns or None,
                    "name": name,
                    "dangerous_matches": [m.to_dict() for m in matches],
                }
            )
        out["dangerous_roles"] = roles

    for subj, perms in result.subject_entries():
        perms_filtered = [p for p in perms if (p.dangerous or not only_dangerous)]
        if not perms_filtered:
            continue

        subj_entry: Dict[str, Any] = {
            "subject": {
                "kind": subj.kind,
                "name": subj.name,
                "namespace": subj.namespace,
            },
            "permissions": [],
        }

        for p in perms_filtered:
            pe = {
                "scope": p.scope,
                "namespace": p.namespace,
                "rule": {
                    "apiGroups": list(p.rule.api_groups),
                    "resources": list(p.rule.resources),
                    "verbs": list(p.rule.verbs),
                    "resourceNames": list(p.rule.resource_names),
                    "nonResourceURLs": list(p.rule.non_resource_urls),
                },
                "dangerous": p.dangerous,
                "matches": [m.to_dict() for m in p.matches],
            }
            if include_sources:
                pe["sources"] = [s.to_dict() for s in p.sources]
            subj_entry["permissions"].append(pe)

        if workload_map and subj.kind.lower() == "serviceaccount":
            wl = workload_map.get(subj.key, [])
            if wl:
                subj_entry["workloads_using_serviceaccount"] = [
                    {"kind": w.kind, "namespace": w.namespace, "name": w.name, "source": w.source} for w in wl
                ]

        out["subjects"].append(subj_entry)

    return out


# CLI

def build_arg_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        description="Static analysis of Kubernetes RBAC (roles/bindings) with dangerous permission detection.",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )

    p.add_argument(
        "-i", "--input",
        action="append",
        required=True,
        help="Path to RBAC YAML/JSON file or directory (can be repeated).",
    )
    p.add_argument(
        "-c", "--config",
        default=None,
        help="Path to YAML config with dangerous rules. If not set, uses ./dangerous_rules.yaml рядом со скриптом.",
    )
    p.add_argument(
        "-o", "--output",
        default=None,
        help="Output file path. If omitted, prints to stdout.",
    )
    p.add_argument(
        "--format",
        choices=["text", "json"],
        default="text",
        help="Report format.",
    )
    p.add_argument(
        "--only-dangerous",
        action="store_true",
        help="Show only dangerous permissions (filter).",
    )
    p.add_argument(
        "--no-sources",
        action="store_true",
        help="Do not include binding/role sources in report.",
    )
    p.add_argument(
        "--no-role-danger",
        action="store_true",
        help="Do not include dangerous roles list (by content).",
    )
    p.add_argument(
        "--workloads",
        action="append",
        default=[],
        help="Optional path to workload manifests (Pod/Deployment/Job/...). Used to list workloads using dangerous ServiceAccounts.",
    )

    return p


def main(argv: Optional[List[str]] = None) -> int:
    args = build_arg_parser().parse_args(argv)

    input_paths = [Path(x) for x in args.input]
    rbac_files = discover_files(input_paths)
    if not rbac_files:
        print("No RBAC files found in input paths.", file=sys.stderr)
        return 2

    # config path
    if args.config:
        cfg_path = Path(args.config)
    else:
        cfg_path = Path(__file__).resolve().parent / "dangerous_rules.yaml"

    if not cfg_path.exists():
        print(f"Config file not found: {cfg_path}", file=sys.stderr)
        return 2

    config = AnalyzerConfig.load(cfg_path)

    roles, clusterroles, bindings, warnings = parse_rbac_objects(rbac_files,)

    apply_clusterrole_aggregation(clusterroles, warnings)

    analyzer = RBACAnalyzer(config)
    result = analyzer.analyze(roles, clusterroles, bindings, warnings)

    workload_map = None
    if args.workloads:
        wl_files = discover_files([Path(x) for x in args.workloads])
        if wl_files:
            workload_map, wl_warnings = parse_workloads(wl_files)
            result.warnings.extend(wl_warnings)

    include_sources = not args.no_sources
    include_role_danger = not args.no_role_danger

    if args.format == "json":
        report_obj = build_json_report(result, args.only_dangerous, include_sources, include_role_danger, workload_map)
        txt = json.dumps(report_obj, ensure_ascii=False, indent=2)
    else:
        txt = render_text_report(result, args.only_dangerous, include_sources, include_role_danger, workload_map)

    if args.output:
        Path(args.output).write_text(txt, encoding="utf-8")
    else:
        print(txt)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
