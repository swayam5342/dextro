import { useState, type FormEvent } from "react";

import { Field, FormError } from "@/components/field";
import { Button } from "@/components/ui/button";

import { useCreateS3Connection } from "../queries";

const EMPTY = { name: "", endpoint: "", bucket: "", region: "", accessKeyId: "", secretAccessKey: "" };

export function S3Form() {
  const [values, setValues] = useState(EMPTY);
  const create = useCreateS3Connection();

  function set(field: keyof typeof EMPTY, value: string) {
    setValues((current) => ({ ...current, [field]: value }));
  }

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    create.mutate(
      {
        name: values.name,
        endpoint: values.endpoint,
        bucket: values.bucket,
        region: values.region || undefined,
        access_key_id: values.accessKeyId,
        secret_access_key: values.secretAccessKey,
      },
      { onSuccess: () => setValues(EMPTY) },
    );
  }

  return (
    <form onSubmit={handleSubmit} className="max-w-sm space-y-4">
      <Field
        id="s3-name"
        label="Name"
        hint="What to call this bucket in dextro."
        required
        value={values.name}
        onChange={(event) => set("name", event.target.value)}
      />
      <Field
        id="s3-endpoint"
        label="Endpoint"
        type="url"
        placeholder="https://s3.example.com"
        required
        value={values.endpoint}
        onChange={(event) => set("endpoint", event.target.value)}
      />
      <Field
        id="s3-bucket"
        label="Bucket"
        hint="It must already exist. dextro keeps its files under a dextro/ folder."
        required
        value={values.bucket}
        onChange={(event) => set("bucket", event.target.value)}
      />
      <Field
        id="s3-region"
        label="Region"
        hint="Leave this empty to use us-east-1."
        value={values.region}
        onChange={(event) => set("region", event.target.value)}
      />
      <Field
        id="s3-access-key"
        label="Access key ID"
        autoComplete="off"
        required
        value={values.accessKeyId}
        onChange={(event) => set("accessKeyId", event.target.value)}
      />
      <Field
        id="s3-secret-key"
        label="Secret access key"
        type="password"
        autoComplete="new-password"
        required
        value={values.secretAccessKey}
        onChange={(event) => set("secretAccessKey", event.target.value)}
      />
      <FormError message={create.error?.message} />
      <Button type="submit" disabled={create.isPending}>
        {create.isPending ? "Checking the bucket…" : "Connect bucket"}
      </Button>
    </form>
  );
}
