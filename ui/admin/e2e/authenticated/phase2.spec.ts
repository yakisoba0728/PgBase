import { applyCollectionEdit, createCollection, createRecord, deleteCollection, expect, test, uniqueName } from "./fixtures";

test("creates and reads an inferred SQL view", async ({ page }, info) => {
  const source=uniqueName("sql_source",info), view=uniqueName("sql_view",info);
  try {
    await createCollection(page,source,[{name:"title"}]);
    await createRecord(page,source,{title:"view result"});
    await page.goto("/admin/collections/new");
    await page.getByLabel("Collection name").fill(view);
    await page.getByLabel("Collection kind").selectOption("view");
    await page.getByLabel("Definition mode").selectOption("sql");
    await page.getByLabel("SQL SELECT").fill(`SELECT id, upper(title)::text AS headline FROM app.${source}`);
    await page.getByRole("button",{name:"Preview collection"}).click();
    await expect(page.getByRole("button",{name:"Apply collection"})).toBeVisible();
    await page.getByRole("button",{name:"Apply collection"}).click();
    await expect(page.getByRole("heading",{name:"Collections",exact:true})).toBeVisible();
    await page.goto(`/admin/collections/${view}/records`);
    await expect(page.getByText("VIEW RESULT",{exact:true})).toBeVisible();
    await expect(page.getByRole("link",{name:"Create record",exact:true})).toHaveCount(0);
    await page.goto(`/admin/collections/${view}/edit`);
    await expect(page.getByLabel("Definition mode")).toBeDisabled();
    await page.getByLabel("SQL SELECT").fill(`SELECT id, title || ' changed' AS headline FROM app.${source}`);
    await applyCollectionEdit(page,view);
    await expect(page.getByText("view result changed",{exact:true})).toBeVisible();
  } finally {await deleteCollection(page,view);await deleteCollection(page,source);}
});

test("creates, downloads and uploads a managed backup", async ({page})=>{
  test.skip(process.env.PGBASE_BROWSER_MANAGED_BACKUPS!=="1","requires isolated managed-backup fixture");
  test.setTimeout(120_000);
  await page.goto("/admin/system");
  await expect(page.getByRole("button",{name:"Create backup",exact:true})).toBeVisible();
  const creation=page.waitForResponse(response=>response.url().endsWith("/api/admin/backups")&&response.request().method()==="POST");
  await page.getByRole("button",{name:"Create backup",exact:true}).click();
  const job=await (await creation).json();
  await expect(page.getByText(new RegExp(`create backup ${job.backupId}: completed`))).toBeVisible({timeout:60_000});
  const download=page.waitForEvent("download");
  await page.getByRole("button",{name:"Download",exact:true}).first().click();
  const file=await download,path=await file.path();
  expect(path).not.toBeNull();
  await page.getByLabel("Upload gzip backup").setInputFiles(path!);
  await expect(page.getByText("Backup uploaded.",{exact:true})).toBeVisible({timeout:60_000});
  await expect(page.getByRole("button",{name:"Download",exact:true})).toHaveCount(2);
  // This fixture's entire managed archive directory is disposable. Exercise
  // authorized deletion through the same buttons after verifying both entries.
  await page.getByRole("button",{name:"Delete",exact:true}).first().click();
  await expect(page.getByRole("button",{name:"Download",exact:true})).toHaveCount(1);
  await page.getByRole("button",{name:"Delete",exact:true}).first().click();
  await expect(page.getByText("No managed backups are available.",{exact:true})).toBeVisible();
});

test("creates a record with required files atomically and replaces them", async ({page},info)=>{
  const collection=uniqueName("required_file",info);
  try {
    await page.goto("/admin/collections/new");
    await page.getByLabel("Collection name").fill(collection);
    await page.getByLabel("Field 1 name").fill("title");
    await page.getByRole("button",{name:"Add field"}).click();
    await page.getByLabel("Field 2 name").fill("attachment");
    await page.getByLabel("Field 2 type").selectOption("file");
    await page.getByLabel("Field 2 required").check();
    await page.getByLabel("Field 2 maximum files").fill("1");
    await page.getByRole("button",{name:"Preview collection"}).click();
    await page.getByRole("button",{name:"Apply collection"}).click();
    await expect(page.getByRole("heading",{name:"Collections",exact:true})).toBeVisible();
    await page.goto(`/admin/collections/${collection}/records/new`);
    await page.getByLabel("title state").selectOption("value");
    await page.getByLabel("title",{exact:true}).fill("with required attachment");
    await page.getByRole("button",{name:"Create record",exact:true}).click();
    await expect(page.getByRole("heading",{name:`Create ${collection} record`,exact:true})).toBeVisible();
    await page.getByLabel("attachment files for save",{exact:true}).setInputFiles({name:"first.txt",mimeType:"text/plain",buffer:Buffer.from("first file")});
    const saved=page.waitForResponse(response=>response.url().endsWith("/api/v1/collections/batch")&&response.request().method()==="POST");
    await page.getByRole("button",{name:"Create record",exact:true}).click();
    const result=await (await saved).json();
    expect(result.results[0].status).toBe(201);
    const id=result.results[0].body.id;
    await expect(page.getByRole("heading",{name:`${collection} records`,exact:true})).toBeVisible();
    await page.goto(`/admin/collections/${collection}/records/${id}`);
    await expect(page.getByText(/first.txt/)).toBeVisible();
    await page.getByLabel("attachment files for save",{exact:true}).setInputFiles({name:"second.txt",mimeType:"text/plain",buffer:Buffer.from("replacement")});
    await page.getByRole("button",{name:"Save changes",exact:true}).click();
    await expect(page.getByRole("heading",{name:`${collection} records`,exact:true})).toBeVisible();
    await page.goto(`/admin/collections/${collection}/records/${id}`);
    await expect(page.getByText(/second.txt/)).toBeVisible();
    await expect(page.getByText(/first.txt/)).toHaveCount(0);
  } finally {await deleteCollection(page,collection);}
});
