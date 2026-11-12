import { useParams } from "react-router";
import { AuthCollectionsIndex, AuthCollectionView } from "../auth/AuthCollectionsView";

/** /auth/collections and /auth/collections/:name/:tab? */
export function AuthCollectionsPage() {
  const { name, tab } = useParams();
  return name ? <AuthCollectionView name={name} tab={tab} /> : <AuthCollectionsIndex />;
}
