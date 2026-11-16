// Mirrors the JSON from internal/apps.

import type { Spec } from "../docker/types";

export interface AppSummary {
  id: string;
  name: string;
  author?: string;
  image: string;
  icon?: string;
  overview: string;
  categories: string[];
  beta?: boolean;
  deprecated?: boolean;
  downloads?: number;
  stars?: number;
  project?: string;
  support?: string;
}

export interface Category {
  id: string;
  label: string;
  count: number;
}

export interface Catalogue {
  updatedAt?: string;
  stale: boolean;
  offline: boolean;
  missing: boolean;
  message?: string;
  appdata: string;
  appdataDefault: string;
  appdataCustom: boolean;
  categories: Category[];
  apps: AppSummary[];
}

export interface AppSettings {
  appdata: string;
  appdataDefault: string;
  appdataCustom: boolean;
}

export interface AppSpec {
  app: AppSummary;
  spec: Spec;
  notes: string[];
}
